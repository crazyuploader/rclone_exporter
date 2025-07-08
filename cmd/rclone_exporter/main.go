package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/crazyuploader/rclone_exporter/internal/exporter"
	"github.com/crazyuploader/rclone_exporter/internal/rclone"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	cli "github.com/urfave/cli/v3"
)

func main() {
	app := &cli.Command{
		Name:  "rclone_exporter",
		Usage: "Prometheus exporter for rclone",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "web.listen-address",
				Usage:   "Address to listen on",
				Value:   ":9116",
				Sources: cli.EnvVars("RC_EXPORTER_LISTEN"),
			},
			&cli.StringFlag{
				Name:    "web.telemetry-path",
				Usage:   "Path to expose metrics",
				Value:   "/metrics",
				Sources: cli.EnvVars("RC_EXPORTER_METRICS"),
			},
			&cli.StringFlag{
				Name:    "web.probe-path",
				Usage:   "Path to expose probe endpoint",
				Value:   "/probe",
				Sources: cli.EnvVars("RC_EXPORTER_PROBE"),
			},
			&cli.StringFlag{
				Name:    "rclone.path",
				Usage:   "Path to the rclone binary",
				Value:   "rclone",
				Sources: cli.EnvVars("RC_EXPORTER_RCLONE_BIN"),
			},
			&cli.DurationFlag{
				Name:    "rclone.timeout",
				Usage:   "Timeout for rclone command",
				Value:   2 * time.Minute,
				Sources: cli.EnvVars("RC_EXPORTER_RCLONE_TIMEOUT"),
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
			// Logger configuration
			if cmd.Bool("log.pretty") {
				log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
			} else {
				log.Logger = log.Output(os.Stderr)
			}

			if cmd.Bool("log.debug") {
				zerolog.SetGlobalLevel(zerolog.DebugLevel)
				log.Debug().Msg("Debug logging enabled")
			} else {
				zerolog.SetGlobalLevel(zerolog.InfoLevel)
			}
			zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

			// Setup rclone client
			rclonePath := cmd.String("rclone.path")
			rcloneTimeout := cmd.Duration("rclone.timeout")
			client := rclone.NewRcloneClientWithConfig(rclonePath, rcloneTimeout)

			if err := client.CheckBinaryAvailable(); err != nil {
				log.Fatal().Err(err).Msg("rclone binary is not accessible or not functioning")
			}

			// Create Prometheus exporter
			exporter := exporter.NewExporter(client)

			// Setup HTTP handlers
			http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "rclone_exporter is running.\nUse %s?remote=<name>\n", cmd.String("web.probe-path"))
			})
			http.Handle(cmd.String("web.telemetry-path"), promhttp.Handler())
			http.HandleFunc(cmd.String("web.probe-path"), exporter.ProbeHandler)

			// HTTP server configuration
			server := &http.Server{Addr: cmd.String("web.listen-address")}

			// Graceful shutdown routine
			idleConnsClosed := make(chan struct{})
			go func() {
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
				<-sigCh

				log.Warn().Msg("Shutdown signal received")

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				if err := server.Shutdown(ctx); err != nil {
					log.Error().Err(err).Msg("HTTP server shutdown failed")
				}
				close(idleConnsClosed)
			}()

			log.Info().
				Str("listen", server.Addr).
				Str("metrics_path", cmd.String("web.telemetry-path")).
				Str("probe_path", cmd.String("web.probe-path")).
				Str("rclone_bin", rclonePath).
				Dur("timeout", rcloneTimeout).
				Msg("rclone_exporter is up and listening")

			// Start server
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatal().Err(err).Msg("HTTP server crashed")
			}

			<-idleConnsClosed
			log.Info().Msg("Exporter shutdown completed")
			return nil
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal().Err(err).Msg("Application startup failed")
	}
}
