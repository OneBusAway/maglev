package restapi

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer initializes the OpenTelemetry tracer provider.
// It configures stdout exporter for development and sets up resource attributes.
// Returns a TracerProvider that should be shutdown when the application exits.
func InitTracer(serviceName, serviceVersion, environment string, logger *slog.Logger) (*trace.TracerProvider, error) {
	// Create stdout exporter for development (can be replaced with OTLP for production)
	exporter, err := stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		return nil, err
	}

	// Create resource with service information
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion),
			semconv.DeploymentEnvironmentKey.String(environment),
		),
	)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to create resource for tracing", "error", err)
		}
		// Continue with empty resource rather than failing
		res = resource.Default()
	}

	// Create tracer provider with batch span processor
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
		// Sample 100% of traces in development, can be adjusted for production
		trace.WithSampler(trace.AlwaysSample()),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator for distributed tracing context
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	if logger != nil {
		logger.Info("OpenTelemetry tracing initialized",
			"service", serviceName,
			"version", serviceVersion,
			"environment", environment,
		)
	}

	return tp, nil
}
