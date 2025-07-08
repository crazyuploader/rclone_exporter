package exporter

import (
	"net/http"
	"time"

	"github.com/crazyuploader/rclone_exporter/internal/rclone"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

// Exporter holds Prometheus metrics and an rclone client.
type Exporter struct {
	rcloneClient         rclone.Client
	rcloneSizeBytes      *prometheus.GaugeVec
	rcloneObjectsCount   *prometheus.GaugeVec
	probeSuccess         prometheus.Gauge
	probeDurationSeconds prometheus.Gauge
	scrapeErrorsTotal    prometheus.Counter
}

// NewExporter registers and returns a new Exporter instance.
func NewExporter(rcloneClient rclone.Client) *Exporter {
	e := &Exporter{
		rcloneClient: rcloneClient,

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
		probeSuccess: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "rclone_probe_success",
				Help: "1 if the rclone size probe succeeded, 0 otherwise.",
			},
		),
		probeDurationSeconds: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "rclone_probe_duration_seconds",
				Help: "Duration of the rclone size probe in seconds.",
			},
		),
		scrapeErrorsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "rclone_exporter_scrape_errors_total",
				Help: "Total number of probe errors.",
			},
		),
	}

	// Register all metrics
	prometheus.MustRegister(
		e.rcloneSizeBytes,
		e.rcloneObjectsCount,
		e.probeSuccess,
		e.probeDurationSeconds,
		e.scrapeErrorsTotal,
	)

	return e
}

// ProbeHandler handles /probe requests and emits metrics for a given remote.
func (e *Exporter) ProbeHandler(w http.ResponseWriter, r *http.Request) {
	e.probeSuccess.Set(0)
	e.probeDurationSeconds.Set(0)

	remote := r.URL.Query().Get("remote")
	if remote == "" {
		http.Error(w, "Missing 'remote' parameter", http.StatusBadRequest)
		e.scrapeErrorsTotal.Inc()
		log.Warn().Str("client", r.RemoteAddr).Msg("Missing 'remote' parameter")
		return
	}

	start := time.Now()
	log.Info().Str("remote", remote).Msg("Starting probe")

	defer func() {
		e.probeDurationSeconds.Set(time.Since(start).Seconds())
	}()

	output, err := e.rcloneClient.GetRemoteSize(remote)
	if err != nil {
		e.scrapeErrorsTotal.Inc()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Error().Err(err).Str("remote", remote).Msg("Probe failed")
		return
	}

	e.rcloneSizeBytes.WithLabelValues(remote).Set(float64(output.Bytes))
	e.rcloneObjectsCount.WithLabelValues(remote).Set(float64(output.Count))
	e.probeSuccess.Set(1)

	log.Info().
		Str("remote", remote).
		Int64("bytes", output.Bytes).
		Int64("count", output.Count).
		Msg("Probe successful")

	promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}
