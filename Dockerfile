#
# Created by Jugal Kishore -- 2025
#
FROM golang:1.24-alpine AS builder

# Enable Go module support & install dependencies
ENV CGO_ENABLED=0

# Set working directory
WORKDIR /app

# Copy source code
COPY . .

# Download dependencies and build the binary
RUN go build -o rclone_exporter ./cmd/rclone_exporter

# Stage 2: Minimal runtime image
FROM alpine:latest

# Install rclone
RUN apk add --no-cache rclone ca-certificates

# Set working directory
WORKDIR /app

# Copy the compiled binary from builder
COPY --from=builder /app/rclone_exporter .

# Expose default Prometheus exporter port
EXPOSE 9116

# Default arguments (can be overridden via CMD or entrypoint)
CMD ["./rclone_exporter"]
