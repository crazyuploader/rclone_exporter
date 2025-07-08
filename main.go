package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os/exec"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	bucket = flag.String("bucket", "", "Name of the rclone S3 bucket (e.g. s3-bom:)")
	port   = flag.Int("port", 9190, "Port to listen on")
)

var s3Usage = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "s3_bucket_usage_bytes",
		Help: "Total bytes used in the S3 bucket according to rclone size",
	},
	[]string{"bucket"},
)

func updateMetric() {
	cmd := exec.Command("rclone", "size", *bucket, "--json")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("Error running rclone: %v", err)
		return
	}

	var result struct {
		Bytes int64 `json:"bytes"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		log.Printf("Error parsing rclone output: %v", err)
		return
	}

	s3Usage.WithLabelValues(*bucket).Set(float64(result.Bytes))
}

func main() {
	flag.Parse()
	if *bucket == "" {
		log.Fatal("You must provide --bucket (e.g. --bucket=s3-bom:)")
	}

	prometheus.MustRegister(s3Usage)

	// Update metric at each scrape
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		updateMetric()
		promhttp.Handler().ServeHTTP(w, r)
	})

	addr := ":" + strconv.Itoa(*port)
	log.Printf("Serving metrics on %s/metrics", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
