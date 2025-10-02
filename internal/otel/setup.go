package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// SetupOTLPMetrics initializes the OpenTelemetry MeterProvider and OTLP HTTP exporter.
func SetupOTLPMetrics(ctx context.Context, serviceName, serviceVersion string) (func(context.Context) error, error) {
	// Create a resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create the OTLP HTTP exporter
	// Configuration can be done via environment variables like OTEL_EXPORTER_OTLP_ENDPOINT
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	// Create a periodic reader
	// Metrics will be collected and exported every 30 seconds by default
	metricReader := metric.NewPeriodicReader(metricExporter, metric.WithInterval(30*time.Second))

	// Create a MeterProvider
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metricReader),
	)

	// Set the global MeterProvider
	otel.SetMeterProvider(meterProvider)

	return meterProvider.Shutdown, nil
}
