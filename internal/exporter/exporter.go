package exporter

import (
	"context"
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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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

	// OpenTelemetry Metrics
	otelScrapeErrorsTotal  metric.Int64Counter
	otelProbeRequestsTotal metric.Int64Counter

	// Last observed values for OpenTelemetry gauges
	lastRcloneSizeBytes      int64
	lastRcloneObjectsCount   int64
	lastProbeSuccess         int64
	lastProbeDurationSeconds float64
}

// NewExporter creates a new Exporter instance with a custom registry.
func NewExporter(rcloneClient rclone.Client) *Exporter {
	registry := prometheus.NewRegistry()

	// Initialize OpenTelemetry Meter
	meter := otel.Meter("rclone_exporter")

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

	// Create OpenTelemetry metric instruments
	otelScrapeErrorsTotal, err := meter.Int64Counter(
		"rclone_exporter_scrape_errors_total",
		metric.WithDescription("Total number of rclone probe errors."),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create otelScrapeErrorsTotal counter")
	}
	e.otelScrapeErrorsTotal = otelScrapeErrorsTotal

	otelProbeRequestsTotal, err := meter.Int64Counter(
		"rclone_exporter_probe_requests_total",
		metric.WithDescription("Total number of probe requests received."),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create otelProbeRequestsTotal counter")
	}
	e.otelProbeRequestsTotal = otelProbeRequestsTotal

	// Register observable gauges
	_, err = meter.Int64ObservableGauge(
		"rclone_remote_size_bytes",
		metric.WithDescription("Total size of the rclone remote in bytes."),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			e.mu.RLock()
			defer e.mu.RUnlock()
			observer.Observe(e.lastRcloneSizeBytes, metric.WithAttributeSet(attribute.NewSet(attribute.String("remote", ""))))
			return nil
		}),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create otelRcloneSizeBytes observable gauge")
	}

	_, err = meter.Int64ObservableGauge(
		"rclone_remote_objects_count",
		metric.WithDescription("Total number of objects in the rclone remote."),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			e.mu.RLock()
			defer e.mu.RUnlock()
			observer.Observe(e.lastRcloneObjectsCount, metric.WithAttributeSet(attribute.NewSet(attribute.String("remote", ""))))
			return nil
		}),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create otelRcloneObjectsCount observable gauge")
	}

	_, err = meter.Int64ObservableGauge(
		"rclone_probe_success",
		metric.WithDescription("1 if the last rclone probe was successful, 0 otherwise."),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			e.mu.RLock()
			defer e.mu.RUnlock()
			observer.Observe(e.lastProbeSuccess, metric.WithAttributeSet(attribute.NewSet(attribute.String("remote", ""))))
			return nil
		}),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create otelProbeSuccess observable gauge")
	}

	_, err = meter.Float64ObservableGauge(
		"rclone_probe_duration_seconds",
		metric.WithDescription("Duration of the rclone size probe in seconds."),
		metric.WithFloat64Callback(func(_ context.Context, observer metric.Float64Observer) error {
			e.mu.RLock()
			defer e.mu.RUnlock()
			observer.Observe(e.lastProbeDurationSeconds, metric.WithAttributeSet(attribute.NewSet(attribute.String("remote", ""))))
			return nil
		}),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create otelProbeDurationSeconds observable gauge")
	}

	// Register all Prometheus metrics with the custom registry
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
	ctx := r.Context()
	attrs := attribute.NewSet(attribute.String("remote", remote))

	e.scrapeErrorsTotal.Inc()
	if e.otelScrapeErrorsTotal != nil {
		e.otelScrapeErrorsTotal.Add(ctx, 1, metric.WithAttributeSet(attrs))
	}

	if remote != "" {
		e.probeSuccess.WithLabelValues(remote).Set(0)
		e.mu.Lock()
		e.lastProbeSuccess = 0
		e.mu.Unlock()
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
	ctx := r.Context()

	e.probeRequestsTotal.Inc()
	if e.otelProbeRequestsTotal != nil {
		e.otelProbeRequestsTotal.Add(ctx, 1)
	}

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
		e.mu.Lock()
		e.lastProbeDurationSeconds = duration
		e.mu.Unlock()

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

	// Update Prometheus metrics with labels
	e.rcloneSizeBytes.WithLabelValues(remote).Set(float64(output.Bytes))
	e.rcloneObjectsCount.WithLabelValues(remote).Set(float64(output.Count))
	e.probeSuccess.WithLabelValues(remote).Set(1)

	// Update last observed values for OpenTelemetry gauges
	e.mu.Lock()
	e.lastRcloneSizeBytes = output.Bytes
	e.lastRcloneObjectsCount = output.Count
	e.lastProbeSuccess = 1
	e.mu.Unlock()

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
