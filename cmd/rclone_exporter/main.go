package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/crazyuploader/rclone_exporter/internal/exporter"
	"github.com/crazyuploader/rclone_exporter/internal/logging"
	"github.com/crazyuploader/rclone_exporter/internal/rclone"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	cli "github.com/urfave/cli/v3"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
	goVersion = runtime.Version()
)

// Constants for better maintainability
const (
	DefaultShutdownTimeout = 10 * time.Second
	DefaultRcloneTimeout   = 2 * time.Minute
	DefaultListenAddress   = ":9116"
	DefaultMetricsPath     = "/metrics"
	DefaultProbePath       = "/probe"
	DefaultRclonePath      = "rclone"
	DefaultHealthPath      = "/health"
	DefaultRemotesPath     = "/remotes"
	DefaultConfigPath      = "/config"
)

// ConfigResponse represents the runtime configuration exposed via /config endpoint
type ConfigResponse struct {
	BuildInfo    BuildInfo       `json:"build_info"`
	ServerConfig ServerConfig    `json:"server_config"`
	RcloneConfig RcloneConfig    `json:"rclone_config"`
	RuntimeInfo  RuntimeInfo     `json:"runtime_info"`
	Endpoints    EndpointsConfig `json:"endpoints"`
}

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

type ServerConfig struct {
	ListenAddress   string `json:"listen_address"`
	ShutdownTimeout string `json:"shutdown_timeout"`
	ReadTimeout     string `json:"read_timeout"`
	WriteTimeout    string `json:"write_timeout"`
	IdleTimeout     string `json:"idle_timeout"`
}

type RcloneConfig struct {
	BinaryPath string `json:"binary_path"`
	Timeout    string `json:"timeout"`
	Version    string `json:"version,omitempty"`
}

type RuntimeInfo struct {
	Uptime        string `json:"uptime"`
	NumGoroutines int    `json:"num_goroutines"`
	NumCPU        int    `json:"num_cpu"`
	GoMemStats    string `json:"go_mem_stats,omitempty"`
}

type EndpointsConfig struct {
	MetricsPath string `json:"metrics_path"`
	ProbePath   string `json:"probe_path"`
	HealthPath  string `json:"health_path"`
	RemotesPath string `json:"remotes_path"`
	ConfigPath  string `json:"config_path"`
}

type LandingPageData struct {
	Version     string
	Commit      string
	BuildDate   string
	GoVersion   string
	Uptime      string
	MetricsPath string
	ProbePath   string
	HealthPath  string
	RemotesPath string
	ConfigPath  string
}

var startTime = time.Now()

// HTML template for landing page
const landingPageTemplate = `<!DOCTYPE html>
<html lang="en">
    <head>
        <meta charset="UTF-8">
        <meta name="viewport" content="width=device-width, initial-scale=1.0">
        <title>Rclone Exporter</title>
        <style>
            body {
            font-family: sans-serif;
            margin: 40px auto;
            max-width: 700px;
            line-height: 1.6;
            color: #222;
            }
            h1 {
            text-align: center;
            font-size: 1.8em;
            margin-bottom: 0.2em;
            }
            h2 {
            margin-top: 1.5em;
            font-size: 1.2em;
            }
            .subtitle {
            text-align: center;
            color: #555;
            margin-bottom: 1.5em;
            }
            .info {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
            gap: 10px;
            }
            .info div {
            padding: 8px;
            border: 1px solid #ddd;
            border-radius: 4px;
            text-align: center;
            }
            ul {
            list-style: none;
            padding-left: 0;
            }
            li {
            margin: 6px 0;
            }
            a {
            color: #0044cc;
            text-decoration: none;
            }
            a:hover {
            text-decoration: underline;
            }
            code {
            background: #f5f5f5;
            padding: 2px 5px;
            border-radius: 3px;
            }
            footer {
            text-align: center;
            font-size: 0.9em;
            color: #666;
            margin-top: 2em;
            border-top: 1px solid #eee;
            padding-top: 1em;
            }
        </style>
    </head>
    <body>
        <h1>Rclone Exporter</h1>
        <p class="subtitle">Prometheus exporter for rclone remote monitoring</p>
        <div class="info">
            <div><strong>Version</strong><br>{{.Version}}</div>
            <div><strong>Commit</strong><br>{{.Commit}}</div>
            <div><strong>Go Version</strong><br>{{.GoVersion}}</div>
            <div><strong>Uptime</strong><br>{{.Uptime}}</div>
        </div>
        <h2>Available Endpoints</h2>
        <ul>
            <li><a href="{{.MetricsPath}}">{{.MetricsPath}}</a> — metrics</li>
            <li><a href="{{.ProbePath}}">{{.ProbePath}}</a> — probe remote</li>
            <li><a href="{{.HealthPath}}">{{.HealthPath}}</a> — health check</li>
            <li><a href="{{.RemotesPath}}">{{.RemotesPath}}</a> — list remotes</li>
            <li><a href="{{.ConfigPath}}">{{.ConfigPath}}</a> — exporter config</li>
        </ul>
        <h2>Usage Example</h2>
        <p>Probe a specific remote:</p>
        <p><code>{{.ProbePath}}?remote=&lt;remote_name&gt;</code></p>
        <p>Example: <code>{{.ProbePath}}?remote=myremote:</code></p>
        <footer>
            Built with Go • Build Date: {{.BuildDate}}
        </footer>
    </body>
</html>
`

// createBuildInfoMetric creates and registers the build info metric
func createBuildInfoMetric(registry *prometheus.Registry) {
	buildInfo := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "rclone_exporter",
			Name:      "build_info",
			Help:      "Build information about the rclone exporter including version, commit, and build date",
		},
		[]string{"version", "commit", "build_date", "go_version"},
	)

	buildInfo.WithLabelValues(version, commit, buildDate, goVersion).Set(1)

	registry.MustRegister(buildInfo)
}

// landingPageHandler serves an HTML landing page
func landingPageHandler(cmd *cli.Command) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only serve HTML for root path
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		tmpl, err := template.New("landing").Parse(landingPageTemplate)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse landing page template")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		data := LandingPageData{
			Version:     version,
			Commit:      commit,
			BuildDate:   buildDate,
			GoVersion:   goVersion,
			Uptime:      time.Since(startTime).Round(time.Second).String(),
			MetricsPath: cmd.String("web.telemetry-path"),
			ProbePath:   cmd.String("web.probe-path"),
			HealthPath:  cmd.String("web.health-path"),
			RemotesPath: cmd.String("web.remotes-path"),
			ConfigPath:  cmd.String("web.config-path"),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			log.Error().Err(err).Msg("Failed to execute landing page template")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}

// healthHandler provides a simple health check endpoint with build info
func healthHandler(w http.ResponseWriter, r *http.Request) {
	resp := map[string]string{
		"status":     "OK",
		"version":    version,
		"commit":     commit,
		"build_date": buildDate,
		"go_version": goVersion,
		"uptime":     time.Since(startTime).Round(time.Second).String(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// configHandler exposes the runtime configuration of the exporter
func configHandler(cmd *cli.Command, rcloneClient rclone.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get rclone version (best effort)
		rcloneVersion, _ := rcloneClient.GetVersion()

		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		config := ConfigResponse{
			BuildInfo: BuildInfo{
				Version:   version,
				Commit:    commit,
				BuildDate: buildDate,
				GoVersion: goVersion,
			},
			ServerConfig: ServerConfig{
				ListenAddress:   cmd.String("web.listen-address"),
				ShutdownTimeout: cmd.Duration("server.shutdown-timeout").String(),
				ReadTimeout:     "15s",
				WriteTimeout:    "15s",
				IdleTimeout:     "60s",
			},
			RcloneConfig: RcloneConfig{
				BinaryPath: cmd.String("rclone.path"),
				Timeout:    cmd.Duration("rclone.timeout").String(),
				Version:    rcloneVersion,
			},
			RuntimeInfo: RuntimeInfo{
				Uptime:        time.Since(startTime).Round(time.Second).String(),
				NumGoroutines: runtime.NumGoroutine(),
				NumCPU:        runtime.NumCPU(),
				GoMemStats:    fmt.Sprintf("Alloc=%dMB TotalAlloc=%dMB Sys=%dMB", m.Alloc/1024/1024, m.TotalAlloc/1024/1024, m.Sys/1024/1024),
			},
			Endpoints: EndpointsConfig{
				MetricsPath: cmd.String("web.telemetry-path"),
				ProbePath:   cmd.String("web.probe-path"),
				HealthPath:  cmd.String("web.health-path"),
				RemotesPath: cmd.String("web.remotes-path"),
				ConfigPath:  cmd.String("web.config-path"),
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(config); err != nil {
			log.Error().Err(err).Msg("Failed to encode config response")
			http.Error(w, "Failed to encode configuration", http.StatusInternalServerError)
		}
	}
}

// runServer initializes the rclone client, sets up HTTP handlers, and starts the server
func runServer(_ context.Context, cmd *cli.Command) error {
	// Setup rclone client
	rclonePath := cmd.String("rclone.path")
	rcloneTimeout := cmd.Duration("rclone.timeout")
	client := rclone.NewRcloneClientWithConfig(rclonePath, rcloneTimeout)

	if err := client.CheckBinaryAvailable(); err != nil {
		return fmt.Errorf("rclone binary is not accessible or not functioning: %w", err)
	}

	// Create Prometheus exporter
	exp := exporter.NewExporter(client)
	defer exp.Close() // Ensure cleanup

	// Add build info metric to the exporter's registry
	createBuildInfoMetric(exp.Registry())

	// Handler for /remotes endpoint
	remotesHandler := func(w http.ResponseWriter, r *http.Request) {
		remotes, err := client.ListRemotes()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list remotes: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		// Gather metadata
		remoteCount := len(remotes)
		timestamp := time.Now().UTC().Format(time.RFC3339)
		resp := map[string]interface{}{
			"remotes":      remotes,
			"remote_count": remoteCount,
			"timestamp":    timestamp,
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "Failed to encode remotes as JSON", http.StatusInternalServerError)
		}
	}

	// Setup HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/", landingPageHandler(cmd))
	mux.Handle(cmd.String("web.telemetry-path"), promhttp.HandlerFor(exp.Registry(), promhttp.HandlerOpts{}))
	mux.HandleFunc(cmd.String("web.probe-path"), exp.ProbeHandler)
	mux.HandleFunc(cmd.String("web.health-path"), healthHandler)
	mux.HandleFunc(cmd.String("web.remotes-path"), remotesHandler)
	mux.HandleFunc(cmd.String("web.config-path"), configHandler(cmd, client))

	// HTTP server configuration
	server := &http.Server{
		Addr:         cmd.String("web.listen-address"),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown routine
	idleConnsClosed := make(chan struct{})
	go func() {
		defer close(idleConnsClosed)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh

		log.Warn().Msg("Shutdown signal received")
		shutdownTimeout := cmd.Duration("server.shutdown-timeout")
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("HTTP server shutdown failed")
		}
	}()

	log.Info().
		Str("version", version).
		Str("commit", commit).
		Str("build_date", buildDate).
		Str("go_version", goVersion).
		Msg("Starting rclone_exporter")

	log.Info().
		Str("listen", server.Addr).
		Str("metrics_path", cmd.String("web.telemetry-path")).
		Str("probe_path", cmd.String("web.probe-path")).
		Str("health_path", cmd.String("web.health-path")).
		Str("remotes_path", cmd.String("web.remotes-path")).
		Str("config_path", cmd.String("web.config-path")).
		Str("rclone_bin", rclonePath).
		Dur("timeout", rcloneTimeout).
		Msg("rclone_exporter is up and listening")

	// Start server
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server crashed: %w", err)
	}

	<-idleConnsClosed
	log.Info().Msg("Exporter shutdown completed")
	return nil
}

// main function initializes the CLI application and starts the server
func main() {
	app := &cli.Command{
		Name:    "rclone_exporter",
		Usage:   "Prometheus exporter for rclone",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "web.listen-address",
				Usage:   "Address to listen on",
				Value:   DefaultListenAddress,
				Sources: cli.EnvVars("RC_EXPORTER_LISTEN"),
			},
			&cli.StringFlag{
				Name:    "web.telemetry-path",
				Usage:   "Path to expose metrics",
				Value:   DefaultMetricsPath,
				Sources: cli.EnvVars("RC_EXPORTER_METRICS"),
			},
			&cli.StringFlag{
				Name:    "web.probe-path",
				Usage:   "Path to expose probe endpoint",
				Value:   DefaultProbePath,
				Sources: cli.EnvVars("RC_EXPORTER_PROBE"),
			},
			&cli.StringFlag{
				Name:    "web.health-path",
				Usage:   "Path to expose health check endpoint",
				Value:   DefaultHealthPath,
				Sources: cli.EnvVars("RC_EXPORTER_HEALTH"),
			},
			&cli.StringFlag{
				Name:    "web.remotes-path",
				Usage:   "Path to expose remotes endpoint",
				Value:   DefaultRemotesPath,
				Sources: cli.EnvVars("RC_EXPORTER_REMOTES"),
			},
			&cli.StringFlag{
				Name:    "web.config-path",
				Usage:   "Path to expose configuration endpoint",
				Value:   DefaultConfigPath,
				Sources: cli.EnvVars("RC_EXPORTER_CONFIG"),
			},
			&cli.StringFlag{
				Name:    "rclone.path",
				Usage:   "Path to the rclone binary",
				Value:   DefaultRclonePath,
				Sources: cli.EnvVars("RC_EXPORTER_RCLONE_BIN"),
			},
			&cli.DurationFlag{
				Name:    "rclone.timeout",
				Usage:   "Timeout for rclone command",
				Value:   DefaultRcloneTimeout,
				Sources: cli.EnvVars("RC_EXPORTER_RCLONE_TIMEOUT"),
			},
			&cli.DurationFlag{
				Name:    "server.shutdown-timeout",
				Usage:   "Timeout for graceful server shutdown",
				Value:   DefaultShutdownTimeout,
				Sources: cli.EnvVars("RC_EXPORTER_SHUTDOWN_TIMEOUT"),
			},
			&cli.BoolFlag{
				Name:    "log.pretty",
				Usage:   "Enable human-readable log format",
				Value:   false,
				Sources: cli.EnvVars("RC_EXPORTER_LOG_PRETTY"),
			},
			&cli.BoolFlag{
				Name:    "log.debug",
				Usage:   "Enable debug-level logging",
				Value:   false,
				Sources: cli.EnvVars("RC_EXPORTER_LOG_DEBUG"),
			},
			&cli.StringFlag{
				Name:    "log.file",
				Usage:   "Log file path (optional, logs to file if specified)",
				Value:   "",
				Sources: cli.EnvVars("RC_EXPORTER_LOG_FILE"),
			},
			&cli.BoolFlag{
				Name:    "log.trace",
				Usage:   "Enable trace-level logging (most verbose)",
				Value:   false,
				Sources: cli.EnvVars("RC_EXPORTER_LOG_TRACE"),
			},
			&cli.BoolFlag{
				Name:    "log.warn",
				Usage:   "Set log level to warn and above only",
				Value:   false,
				Sources: cli.EnvVars("RC_EXPORTER_LOG_WARN"),
			},
			&cli.BoolFlag{
				Name:    "log.error",
				Usage:   "Set log level to error only",
				Value:   false,
				Sources: cli.EnvVars("RC_EXPORTER_LOG_ERROR"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := logging.InitLogging(cmd); err != nil {
				return fmt.Errorf("failed to setup logging: %w", err)
			}

			return runServer(ctx, cmd)
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal().Err(err).Msg("Application startup failed")
	}
}
