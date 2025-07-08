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
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "rclone-exporter",
		Usage: "Prometheus exporter for rclone",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "web.listen-address",
				Usage:   "Address to listen on",
				Value:   ":9116",
				EnvVars: []string{"RC_EXPORTER_LISTEN"},
			},
			&cli.StringFlag{
				Name:    "web.telemetry-path",
				Usage:   "Path to expose metrics",
				Value:   "/metrics",
				EnvVars: []string{"RC_EXPORTER_METRICS"},
			},
			&cli.StringFlag{
				Name:    "web.probe-path",
				Usage:   "Path to expose probe endpoint",
				Value:   "/probe",
				EnvVars: []string{"RC_EXPORTER_PROBE"},
			},
			&cli.StringFlag{
				Name:    "rclone.path",
				Usage:   "Path to the rclone binary",
				Value:   "rclone",
				EnvVars: []string{"RC_EXPORTER_RCLONE_BIN"},
			},
			&cli.DurationFlag{
				Name:    "rclone.timeout",
				Usage:   "Timeout for rclone command",
				Value:   2 * time.Minute,
				EnvVars: []string{"RC_EXPORTER_RCLONE_TIMEOUT"},
			},
			&cli.BoolFlag{
				Name:    "log.pretty",
				Usage:   "Enable human-friendly log format",
				Value:   false,
				EnvVars: []string{"RC_EXPORTER_LOG_PRETTY"},
			},
		},
		Action: func(c *cli.Context) error {
			// Logger setup
			if c.Bool("log.pretty") {
				log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
			} else {
				log.Logger = log.Output(os.Stderr)
			}
			zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

			// Rclone client setup
			rclonePath := c.String("rclone.path")
			rcloneTimeout := c.Duration("rclone.timeout")
			client := rclone.NewRcloneClientWithConfig(rclonePath, rcloneTimeout)

			exporter := exporter.NewExporter(client)

			// HTTP handler setup
			http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "rclone-exporter is running.\nUse %s?remote=<name>\n", c.String("web.probe-path"))
			})
			http.Handle(c.String("web.telemetry-path"), promhttp.Handler())
			http.HandleFunc(c.String("web.probe-path"), exporter.ProbeHandler)

			// Server setup
			server := &http.Server{Addr: c.String("web.listen-address")}

			// Graceful shutdown
			idleConnsClosed := make(chan struct{})
			go func() {
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

				<-sigCh
				log.Warn().Msg("Shutdown signal received")
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					log.Error().Err(err).Msg("HTTP shutdown failed")
				}
				close(idleConnsClosed)
			}()

			log.Info().
				Str("listen", server.Addr).
				Str("metrics", c.String("web.telemetry-path")).
				Str("probe", c.String("web.probe-path")).
				Str("rclone", rclonePath).
				Dur("timeout", rcloneTimeout).
				Msg("Starting rclone-exporter")

			if err := server.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatal().Err(err).Msg("HTTP server crashed")
			}

			<-idleConnsClosed
			log.Info().Msg("Exporter exited cleanly")
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Msg("Application failed")
	}
}
