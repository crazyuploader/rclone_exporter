package main

import (
	"flag" // For parsing command-line flags
	"fmt"
	"log"      // For application-level logging
	"net/http" // For creating and managing the HTTP server

	"github.com/crazyuploader/rclone_exporter/internal/exporter" // Our exporter package
	"github.com/crazyuploader/rclone_exporter/internal/rclone"   // Our rclone client package
	"github.com/prometheus/client_golang/prometheus/promhttp"    // Provides the HTTP handler for /metrics
)

var (
	// Define command-line flags using the 'flag' package.
	// These variables will hold the values parsed from the command line.
	// `flag.String` takes: (1) flag name, (2) default value, (3) help message.
	listenAddress = flag.String("web.listen-address", ":9116", "Address to listen on for HTTP requests.")
	metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	probePath     = flag.String("web.probe-path", "/probe", "Path under which to expose probe endpoint.")
)

func main() {
	// Parse command-line flags.
	// This must be called before using the flag variables.
	flag.Parse()

	// 1. Initialize the rclone client.
	// This creates an instance of our rclone.Client implementation.
	rcloneClient := rclone.NewRcloneClient()

	// 2. Initialize the Prometheus exporter.
	// We pass the rcloneClient to the exporter, demonstrating dependency injection.
	// The exporter now has everything it needs to fetch rclone data.
	rcloneExporter := exporter.NewExporter(rcloneClient)

	// 3. Register HTTP handlers for our web server.
	// These handlers define what happens when an HTTP request hits a specific path.

	// The first handler is for the root path ("/").
	// This is a simple health check or welcome message.
	// It responds with a message indicating that the exporter is running
	// and provides instructions on how to use the /probe endpoint.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "rclone-exporter is running. Use /probe?remote=name to fetch remote metrics.")
	})

	// Handler for the main /metrics endpoint.
	// This serves general exporter metrics (like scrape_errors_total)
	// and any other metrics registered with the default Prometheus registry.
	// promhttp.Handler() is a convenient handler provided by the Prometheus client library.
	http.Handle(*metricsPath, promhttp.Handler())

	// Handler for the /probe endpoint.
	// This is where Prometheus will send requests for specific rclone remotes.
	// We use rcloneExporter.ProbeHandler, which is a method on our Exporter struct.
	http.HandleFunc(*probePath, rcloneExporter.ProbeHandler)

	// Log startup information.
	log.Printf("Starting rclone exporter on %s", *listenAddress)
	log.Printf("Metrics exposed on %s", *metricsPath)
	log.Printf("Probe endpoint on %s", *probePath)

	// Start the HTTP server.
	// http.ListenAndServe blocks indefinitely until the server stops (e.g., due to an error).
	// If it returns an error (e.g., port already in use), log.Fatal will print the error
	// and terminate the application.
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
