package restapi

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer initializes the OpenTelemetry tracer provider with environment-appropriate configuration.
// In development, it uses stdout exporter with 100% sampling.
// In production, it defaults to a parent-based ratio sampler (10%) or can be configured via OTEL_* env vars.
// Returns nil if tracing is disabled via environment configuration.
// Returns a TracerProvider that should be shutdown when the application exits.
func InitTracer(serviceName, serviceVersion, environment string, logger *slog.Logger) (*trace.TracerProvider, error) {
	// Check if tracing is explicitly disabled
	if disabled := os.Getenv("OTEL_SDK_DISABLED"); disabled == "true" {
		if logger != nil {
			logger.Info("OpenTelemetry tracing disabled via OTEL_SDK_DISABLED")
		}
		return nil, nil
	}

	// In production, default to disabled unless explicitly enabled
	if environment == "production" {
		if enabled := os.Getenv("OTEL_TRACING_ENABLED"); enabled != "true" {
			if logger != nil {
				logger.Info("OpenTelemetry tracing disabled in production (set OTEL_TRACING_ENABLED=true to enable)")
			}
			return nil, nil
		}
	}

	// Create exporter (stdout for development, can be configured via env vars)
	exporter, err := createExporter(environment, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
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

	// Create appropriate sampler based on environment
	sampler := createSampler(environment, logger)

	// Create tracer provider with batch span processor
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
		trace.WithSampler(sampler),
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
			"sampler", fmt.Sprintf("%T", sampler),
		)
	}

	return tp, nil
}

// createExporter creates an appropriate trace exporter based on environment.
// Development uses stdout, production can use OTLP or other exporters.
func createExporter(environment string, logger *slog.Logger) (trace.SpanExporter, error) {
	// Check for OTEL_EXPORTER_OTLP_ENDPOINT for OTLP exporter
	// For now, always use stdout in development
	if environment == "development" || environment == "dev" {
		return stdouttrace.New(
			stdouttrace.WithPrettyPrint(),
		)
	}

	// In production, use stdout but log a warning
	// In a real deployment, you would configure OTLP exporter here
	if logger != nil {
		logger.Warn("using stdout exporter in production - consider configuring OTLP exporter for production use")
	}

	return stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
	)
}

// createSampler creates an appropriate sampler based on environment and configuration.
func createSampler(environment string, logger *slog.Logger) trace.Sampler {
	// Check for OTEL_TRACES_SAMPLER env var
	if samplerType := os.Getenv("OTEL_TRACES_SAMPLER"); samplerType != "" {
		switch samplerType {
		case "always_on":
			return trace.AlwaysSample()
		case "always_off":
			return trace.NeverSample()
		case "traceidratio":
			ratio := getTraceSamplerRatio(logger)
			return trace.TraceIDRatioBased(ratio)
		case "parentbased_always_on":
			return trace.ParentBased(trace.AlwaysSample())
		case "parentbased_always_off":
			return trace.ParentBased(trace.NeverSample())
		case "parentbased_traceidratio":
			ratio := getTraceSamplerRatio(logger)
			return trace.ParentBased(trace.TraceIDRatioBased(ratio))
		}
	}

	// Default based on environment
	if environment == "development" || environment == "dev" {
		// 100% sampling in development
		return trace.AlwaysSample()
	}

	// Production default: parent-based with 10% ratio sampling
	// Respects parent trace decision while sampling 10% of root traces
	return trace.ParentBased(trace.TraceIDRatioBased(0.1))
}

// getTraceSamplerRatio reads the OTEL_TRACES_SAMPLER_ARG environment variable
// and returns the sampling ratio. Defaults to 0.1 (10%) if not set or invalid.
func getTraceSamplerRatio(logger *slog.Logger) float64 {
	ratioStr := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if ratioStr == "" {
		return 0.1 // Default 10%
	}

	ratio, err := strconv.ParseFloat(ratioStr, 64)
	if err != nil {
		if logger != nil {
			logger.Warn("invalid OTEL_TRACES_SAMPLER_ARG, using default 0.1",
				"value", ratioStr,
				"error", err,
			)
		}
		return 0.1
	}

	if ratio < 0.0 || ratio > 1.0 {
		if logger != nil {
			logger.Warn("OTEL_TRACES_SAMPLER_ARG out of range [0.0, 1.0], using default 0.1",
				"value", ratio,
			)
		}
		return 0.1
	}

	return ratio
}
