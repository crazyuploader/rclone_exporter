package exporter

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/crazyuploader/rclone_exporter/internal/rclone"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

const (
	MaxRemoteNameLength = 255
	MaxConcurrentProbes = 10
)

var (
	// Regex for validating remote names (basic alphanumeric with common chars)
	remoteNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.:/]+$`)
)

// Exporter defines Prometheus metrics and wraps an rclone client.
type Exporter struct {
	rcloneClient         rclone.Client
	rcloneSizeBytes      *prometheus.GaugeVec
	rcloneObjectsCount   *prometheus.GaugeVec
	probeSuccess         *prometheus.GaugeVec
	probeDurationSeconds *prometheus.GaugeVec
	scrapeErrorsTotal    prometheus.Counter
	probeRequestsTotal   prometheus.Counter
	registry             *prometheus.Registry
	semaphore            chan struct{}
	mu                   sync.RWMutex
}

// NewExporter creates a new Exporter instance with a custom registry.
func NewExporter(rcloneClient rclone.Client) *Exporter {
	registry := prometheus.NewRegistry()

	e := &Exporter{
		rcloneClient: rcloneClient,
		registry:     registry,
		semaphore:    make(chan struct{}, MaxConcurrentProbes),

		rcloneSizeBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rclone_remote_size_bytes",
				Help: "Total size of the rclone remote in bytes.",
			},
			[]string{"remote"},
		),

		rcloneObjectsCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rclone_remote_objects_count",
				Help: "Total number of objects in the rclone remote.",
			},
			[]string{"remote"},
		),

		probeSuccess: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rclone_probe_success",
				Help: "1 if the last rclone probe was successful, 0 otherwise.",
			},
			[]string{"remote"},
		),

		probeDurationSeconds: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rclone_probe_duration_seconds",
				Help: "Duration of the rclone size probe in seconds.",
			},
			[]string{"remote"},
		),

		scrapeErrorsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "rclone_exporter_scrape_errors_total",
				Help: "Total number of rclone probe errors.",
			},
		),

		probeRequestsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "rclone_exporter_probe_requests_total",
				Help: "Total number of probe requests received.",
			},
		),
	}

	// Register all metrics with the custom registry
	registry.MustRegister(
		e.rcloneSizeBytes,
		e.rcloneObjectsCount,
		e.probeSuccess,
		e.probeDurationSeconds,
		e.scrapeErrorsTotal,
		e.probeRequestsTotal,
	)

	return e
}

// Close unregisters all metrics to prevent memory leaks
func (e *Exporter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Unregister from custom registry
	if e.registry != nil {
		e.registry.Unregister(e.rcloneSizeBytes)
		e.registry.Unregister(e.rcloneObjectsCount)
		e.registry.Unregister(e.probeSuccess)
		e.registry.Unregister(e.probeDurationSeconds)
		e.registry.Unregister(e.scrapeErrorsTotal)
		e.registry.Unregister(e.probeRequestsTotal)
	}
}

// validateRemote validates the remote parameter
func (e *Exporter) validateRemote(remote string) error {
	if remote == "" {
		return fmt.Errorf("remote name cannot be empty")
	}

	if len(remote) > MaxRemoteNameLength {
		return fmt.Errorf("remote name too long (max %d characters)", MaxRemoteNameLength)
	}

	if !remoteNameRegex.MatchString(remote) {
		return fmt.Errorf("remote name contains invalid characters")
	}

	return nil
}

// handleError provides consistent error handling
func (e *Exporter) handleError(w http.ResponseWriter, r *http.Request, remote, message string, status int, err error) {
	e.scrapeErrorsTotal.Inc()

	if remote != "" {
		e.probeSuccess.WithLabelValues(remote).Set(0)
	}

	http.Error(w, message, status)

	logEvent := log.Warn().
		Str("client", r.RemoteAddr).
		Str("remote", remote).
		Str("user_agent", r.UserAgent())

	if err != nil {
		logEvent = logEvent.Err(err)
	}

	logEvent.Msg(message)
}

// ProbeHandler handles /probe requests and emits Prometheus metrics.
func (e *Exporter) ProbeHandler(w http.ResponseWriter, r *http.Request) {
	e.probeRequestsTotal.Inc()

	remote := strings.TrimSpace(r.URL.Query().Get("remote"))
	if err := e.validateRemote(remote); err != nil {
		e.handleError(w, r, remote, fmt.Sprintf("Invalid remote parameter: %v", err), http.StatusBadRequest, err)
		return
	}

	// Rate limiting using semaphore
	select {
	case e.semaphore <- struct{}{}:
		defer func() { <-e.semaphore }()
	default:
		e.handleError(w, r, remote, "Too many concurrent requests", http.StatusTooManyRequests, nil)
		return
	}

	start := time.Now()
	log.Debug().
		Str("remote", remote).
		Str("client", r.RemoteAddr).
		Str("user_agent", r.UserAgent()).
		Msg("Starting rclone probe")

	// Always update probe duration, even on failure
	defer func() {
		duration := time.Since(start).Seconds()
		e.probeDurationSeconds.WithLabelValues(remote).Set(duration)

		log.Debug().
			Str("remote", remote).
			Float64("duration_seconds", duration).
			Msg("Probe completed")
	}()

	output, err := e.rcloneClient.GetRemoteSize(remote)
	if err != nil {
		e.handleError(w, r, remote, "rclone probe failed", http.StatusInternalServerError, err)
		return
	}

	// Update metrics with labels
	e.rcloneSizeBytes.WithLabelValues(remote).Set(float64(output.Bytes))
	e.rcloneObjectsCount.WithLabelValues(remote).Set(float64(output.Count))
	e.probeSuccess.WithLabelValues(remote).Set(1)

	log.Debug().
		Str("remote", remote).
		Int64("bytes", output.Bytes).
		Int64("objects", output.Count).
		Msg("Probe successful")

	// Serve metrics using custom registry
	promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}).ServeHTTP(w, r)
}
