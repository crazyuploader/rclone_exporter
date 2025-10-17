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
	namespace           = "rclone"
)

var (
	// Regex for validating remote names (basic alphanumeric with common chars)
	remoteNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.:/]+$`)
)

// Exporter defines Prometheus metrics and wraps an rclone client.
type Exporter struct {
	rcloneClient       rclone.Client
	scrapeErrorsTotal  prometheus.Counter
	probeRequestsTotal prometheus.Counter
	registry           *prometheus.Registry
	semaphore          chan struct{}
	mu                 sync.RWMutex
}

// NewExporter creates a new Exporter instance with a custom registry.
func NewExporter(rcloneClient rclone.Client) *Exporter {
	registry := prometheus.NewRegistry()

	e := &Exporter{
		rcloneClient: rcloneClient,
		registry:     registry,
		semaphore:    make(chan struct{}, MaxConcurrentProbes),

		scrapeErrorsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "exporter",
				Name:      "scrape_errors_total",
				Help:      "Total number of rclone probe errors.",
			},
		),

		probeRequestsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "exporter",
				Name:      "probe_requests_total",
				Help:      "Total number of probe requests received.",
			},
		),
	}

	// Register only the global counters with the shared registry
	registry.MustRegister(
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

// parseRemoteName extracts the remote name and optional subpath from the remote parameter
func parseRemoteName(remote string) (name, remotePath string) {
	// Split on first colon to get remote name
	parts := strings.SplitN(remote, ":", 2)
	name = parts[0]

	// If there's a subpath after the colon, include it
	if len(parts) > 1 {
		remotePath = parts[1]
		if remotePath == "" {
			remotePath = "/"
		}
	} else {
		remotePath = "/"
	}

	return name, remotePath
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

	// Parse remote to extract name and path for better labeling
	remoteName, remotePath := parseRemoteName(remote)

	// Create a fresh registry for this probe
	probeRegistry := prometheus.NewRegistry()

	// Create metrics for this specific probe with enhanced labels
	sizeBytes := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "remote",
			Name:      "size_bytes",
			Help:      "Total size of the rclone remote in bytes.",
		},
		[]string{"remote", "remote_name", "path"},
	)

	objectsCount := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "remote",
			Name:      "objects_count",
			Help:      "Total number of objects in the rclone remote.",
		},
		[]string{"remote", "remote_name", "path"},
	)

	probeSuccess := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "probe",
			Name:      "success",
			Help:      "Whether the last rclone probe was successful (1 = success, 0 = failure).",
		},
		[]string{"remote", "remote_name"},
	)

	probeDurationSeconds := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "probe",
			Name:      "duration_seconds",
			Help:      "Duration of the rclone size probe in seconds.",
		},
		[]string{"remote", "remote_name"},
	)

	probeInfo := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "probe",
			Name:      "info",
			Help:      "Information about the probe target (always 1).",
		},
		[]string{"remote", "remote_name", "path"},
	)

	// Register probe-specific metrics with the probe registry
	probeRegistry.MustRegister(sizeBytes)
	probeRegistry.MustRegister(objectsCount)
	probeRegistry.MustRegister(probeSuccess)
	probeRegistry.MustRegister(probeDurationSeconds)
	probeRegistry.MustRegister(probeInfo)

	// Also register the global counters so they appear in probe output
	probeRegistry.MustRegister(e.scrapeErrorsTotal)
	probeRegistry.MustRegister(e.probeRequestsTotal)

	// Set probe info metric
	probeInfo.WithLabelValues(remote, remoteName, remotePath).Set(1)

	// Always update probe duration, even on failure
	defer func() {
		duration := time.Since(start).Seconds()
		probeDurationSeconds.WithLabelValues(remote, remoteName).Set(duration)

		log.Debug().
			Str("remote", remote).
			Float64("duration_seconds", duration).
			Msg("Probe completed")
	}()

	output, err := e.rcloneClient.GetRemoteSize(remote)
	if err != nil {
		probeSuccess.WithLabelValues(remote, remoteName).Set(0)
		e.handleError(w, r, remote, "rclone probe failed", http.StatusInternalServerError, err)
		return
	}

	// Update metrics with labels
	sizeBytes.WithLabelValues(remote, remoteName, remotePath).Set(float64(output.Bytes))
	objectsCount.WithLabelValues(remote, remoteName, remotePath).Set(float64(output.Count))
	probeSuccess.WithLabelValues(remote, remoteName).Set(1)

	log.Debug().
		Str("remote", remote).
		Int64("bytes", output.Bytes).
		Int64("objects", output.Count).
		Msg("Probe successful")

	// Serve metrics using the probe-specific registry
	promhttp.HandlerFor(probeRegistry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}).ServeHTTP(w, r)
}
