package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/crazyuploader/rclone_exporter/internal/exporter"
	"github.com/crazyuploader/rclone_exporter/internal/logging"
	"github.com/crazyuploader/rclone_exporter/internal/rclone"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	cli "github.com/urfave/cli/v3"
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
)

// Handler functions for better organization
func rootHandler(probePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "rclone_exporter is running.\nUse %s?remote=<name>\n", probePath)
	}
}

// healthHandler provides a simple health check endpoint
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
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
	mux.HandleFunc("/", rootHandler(cmd.String("web.probe-path")))
	mux.Handle(cmd.String("web.telemetry-path"), promhttp.Handler())
	mux.HandleFunc(cmd.String("web.probe-path"), exp.ProbeHandler)
	mux.HandleFunc(cmd.String("web.health-path"), healthHandler)
	mux.HandleFunc(cmd.String("web.remotes-path"), remotesHandler)

	// HTTP server configuration with security headers
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
		Str("listen", server.Addr).
		Str("metrics_path", cmd.String("web.telemetry-path")).
		Str("probe_path", cmd.String("web.probe-path")).
		Str("health_path", cmd.String("web.health-path")).
		Str("remotes_path", cmd.String("web.remotes-path")).
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
		Name:  "rclone_exporter",
		Usage: "Prometheus exporter for rclone",
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
