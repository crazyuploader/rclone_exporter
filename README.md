# Rclone Exporter

[![Go Report Card](https://goreportcard.com/badge/github.com/crazyuploader/rclone_exporter)](https://goreportcard.com/report/github.com/crazyuploader/rclone_exporter)
[![GoReleaser Release Pipeline](https://github.com/crazyuploader/rclone_exporter/actions/workflows/build-and-release.yml/badge.svg)](https://github.com/crazyuploader/rclone_exporter/actions/workflows/build-and-release.yml)
[![Publish to GitHub Container Registry](https://github.com/crazyuploader/rclone_exporter/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/crazyuploader/rclone_exporter/actions/workflows/docker-publish.yml)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A Prometheus exporter for `rclone`, designed to monitor the size, object count, and other metrics of your configured rclone remote(s).

## 🚀 Features

- **Remote Size & Object Count:** Exposes `rclone_remote_size_bytes` and `rclone_remote_objects_count`.
- **Probe Metrics:** Includes `rclone_probe_success` and `rclone_probe_duration_seconds`.
- **Container-Ready:** Includes a `Dockerfile`.

## 📦 Getting Started

### Prerequisites

- **Go (1.18+):** To build from source.
- **rclone:** Must be installed and configured (`rclone.conf`) on the host running the exporter.
- **Prometheus:** To scrape metrics.

### Installation

#### 1. Build from Source

```code
git clone https://github.com/crazyuploader/rclone_exporter.git
cd rclone_exporter
go build -o rclone_exporter ./cmd/rclone_exporter
```

#### 2. Running the Exporter

```code
./rclone_exporter --web.listen-address=":9116"
```

You can run the exporter as a systemd service using the [unit file](contrib/systemd/rclone_exporter.service) provided in the `contrib/systemd` directory.

Or with Docker:

```code
docker build -t rclone_exporter .
docker run -v ~/.config/rclone:/root/.config/rclone:ro -p 9116:9116 rclone_exporter
```

Verify at `http://localhost:9116/metrics` and `http://localhost:9116/probe?remote=YOUR_REMOTE_NAME`.

### 📊 Prometheus Configuration Example

Configure Prometheus to scrape the exporter using the metrics_path: /probe and relabel_configs to pass the remote name.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: "rclone_exporter"
    metrics_path: /probe
    scrape_interval: 300s
    params:
      module: [rclone_size_probe] # Optional

    static_configs:
      - targets:
          - "gdrive"
          - "s3bucket"
          - "dropbox"

    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_remote

      - source_labels: [__param_remote]
        target_label: instance

      - target_label: __address__
        replacement: "rclone_exporter:9116" # Replace with your exporter's host:port
```

## 🏗️ Contributing

Contributions are welcome! Feel free to open issues or submit pull requests.

## 📄 License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.
