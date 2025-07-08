package exporter

import (
	"log"      // For logging messages (e.g., errors during probes)
	"net/http" // For handling HTTP requests (our web server)
	"time"     // For measuring probe duration

	"github.com/crazyuploader/rclone_exporter/internal/rclone" // Importing the rclone client interface
	"github.com/prometheus/client_golang/prometheus"           // The Prometheus client library
	"github.com/prometheus/client_golang/prometheus/promhttp"  // HTTP handlers for Prometheus metrics
)

// Exporter represents the Prometheus exporter for rclone size metrics.
// It holds references to the rclone client and all the Prometheus metrics.
type Exporter struct {
	rcloneClient rclone.Client // Interface for interacting with rclone commands

	// Prometheus Gauges and Counters for exposing metrics
	rcloneSizeBytes      *prometheus.GaugeVec // GaugeVec allows labeling metrics by remote name
	rcloneObjectsCount   *prometheus.GaugeVec // GaugeVec for object count
	probeSuccess         prometheus.Gauge     // Gauge to indicate if a probe was successful (1) or failed (0)
	probeDurationSeconds prometheus.Gauge     // Gauge to record the duration of the probe
	scrapeErrorsTotal    prometheus.Counter   // Counter for internal exporter errors during scrapes
}

// NewExporter creates and initializes a new Exporter instance.
// It takes an rclone.Client interface, promoting dependency injection.
// All Prometheus metrics are defined and registered here.
func NewExporter(rcloneClient rclone.Client) *Exporter {
	e := &Exporter{
		rcloneClient: rcloneClient, // Inject the rclone client dependency

		// Define the rclone_remote_size_bytes metric.
		// GaugeVec is used because we want to add a 'remote' label to distinguish
		// metrics for different rclone remotes (e.g., rclone_remote_size_bytes{remote="my-s3-bucket"}).
		rcloneSizeBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rclone_remote_size_bytes",
				Help: "Total size of the rclone remote in bytes.",
			},
			[]string{"remote"}, // Define labels that this GaugeVec will use
		),
		// Define the rclone_remote_objects_count metric, similar to size.
		rcloneObjectsCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rclone_remote_objects_count",
				Help: "Total number of objects in the rclone remote.",
			},
			[]string{"remote"}, // Define labels
		),
		// Define probe_success. This is a common metric in Prometheus exporters
		// 1 for success, 0 for failure.
		probeSuccess: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "rclone_probe_success",
				Help: "Displays whether the rclone size probe was successful (1 for success, 0 for failure).",
			},
		),
		// Define probe_duration_seconds. Another common metric for measuring
		// how long the probing process took.
		probeDurationSeconds: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "rclone_probe_duration_seconds",
				Help: "Returns how long the rclone size probe took to complete in seconds.",
			},
		),
		// Define a counter for errors internal to the exporter's scraping logic.
		// This tracks issues like missing parameters, or unmarshalling errors within the exporter.
		scrapeErrorsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "rclone_exporter_scrape_errors_total",
				Help: "Total number of errors encountered during rclone size scrapes.",
			},
		),
	}

	// Register all defined metrics with the default Prometheus registry.
	// This is essential for these metrics to be exposed via the /metrics or /probe endpoints.
	// prometheus.MustRegister panics if registration fails, which is fine for application startup.
	prometheus.MustRegister(e.rcloneSizeBytes)
	prometheus.MustRegister(e.rcloneObjectsCount)
	prometheus.MustRegister(e.probeSuccess)
	prometheus.MustRegister(e.probeDurationSeconds)
	prometheus.MustRegister(e.scrapeErrorsTotal)

	return e
}

// ProbeHandler handles incoming HTTP requests to the /probe endpoint.
// This is the core logic that Prometheus will hit to get metrics for a specific remote.
func (e *Exporter) ProbeHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Reset probe-specific metrics for each new scrape
	// This is crucial because Prometheus collects the *current* state of Gauges.
	// If a probe for 'remoteA' runs, then 'remoteB' runs, and 'remoteA' doesn't run again,
	// its old values would linger if not reset.
	e.probeSuccess.Set(0)         // Start assuming failure for this probe
	e.probeDurationSeconds.Set(0) // Reset duration for this probe

	// Get the 'remote' query parameter from the URL.
	// E.g., for /probe?remote=my-s3-bucket, 'remote' will be "my-s3-bucket".
	remote := r.URL.Query().Get("remote")
	if remote == "" {
		// If 'remote' parameter is missing, return a bad request error.
		http.Error(w, "Missing 'remote' parameter", http.StatusBadRequest)
		log.Printf("Error: Missing 'remote' parameter in probe request from %s", r.RemoteAddr)
		e.scrapeErrorsTotal.Inc() // Increment exporter's internal error counter
		return
	}

	log.Printf("Probing rclone remote: %s", remote)

	start := time.Now() // Record start time for duration metric
	defer func() {
		// This defer function ensures probeDurationSeconds is set when the function exits,
		// regardless of whether it succeeded or failed.
		e.probeDurationSeconds.Set(time.Since(start).Seconds())
	}()

	// Call the rclone client to get the size data for the specified remote.
	rcloneOutput, err := e.rcloneClient.GetRemoteSize(remote)
	if err != nil {
		// If there's an error getting rclone data, log it and return an HTTP 500 error.
		log.Printf("Error probing remote %s: %v", remote, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		e.scrapeErrorsTotal.Inc() // Increment exporter's internal error counter
		return
	}

	// If the rclone command was successful, set the Prometheus metrics.
	// Use WithLabelValues(remote) to attach the 'remote' label to the gauge values.
	e.rcloneSizeBytes.WithLabelValues(remote).Set(float64(rcloneOutput.Bytes))
	e.rcloneObjectsCount.WithLabelValues(remote).Set(float64(rcloneOutput.Count))
	e.probeSuccess.Set(1) // Mark the probe as successful

	// Serve the metrics.
	// promhttp.HandlerFor creates an HTTP handler that serves metrics from a given Prometheus Gatherer.
	// prometheus.DefaultGatherer collects all metrics registered with the default registry.
	// This is effectively serving the Prometheus exposition format (text-based) to the caller.
	h := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}
