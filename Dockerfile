#
# Created by Jugal Kishore -- 2025
#
FROM golang:1.24-alpine AS builder

# Enable Go module support & install dependencies
ENV CGO_ENABLED=0

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Download dependencies and build the binary
RUN go build -o rclone_exporter ./cmd/rclone_exporter

# Stage 2: Get rclone binary
FROM alpine:3.22 AS rclone

# Get architecture
ARG TARGETARCH
ENV TARGETARCH=${TARGETARCH}

# Install rclone
RUN apk add --no-cache curl zip

# Download rclone binary for the specific architecture
RUN case "${TARGETARCH}" in \
    "amd64") curl -L https://downloads.rclone.org/rclone-current-linux-amd64.zip -o rclone.zip ;; \
    "arm64") curl -L https://downloads.rclone.org/rclone-current-linux-arm64.zip -o rclone.zip ;; \
    *) echo "Unsupported architecture: ${TARGETARCH}" && exit 1 ;; \
    esac && \
    unzip rclone.zip && \
    mv rclone-*-linux-* /usr/local/bin/rclone && \
    chmod +x /usr/local/bin/rclone && \
    rm rclone.zip

# Stage 3: Minimal runtime image
FROM alpine:3.22

# Set working directory
WORKDIR /app

# Copy the compiled binary from builder
COPY --from=builder /app/rclone_exporter .

# Copy rclone binary from the previous stage
COPY --from=rclone /usr/local/bin/rclone /usr/local/bin/rclone

# Expose default Prometheus exporter port
EXPOSE 9116

# Default arguments (can be overridden via CMD or entrypoint)
CMD ["./rclone_exporter"]
