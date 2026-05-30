package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// InitTracerProvider wires an OTLP/HTTP exporter to a batching tracer provider
// and registers it globally. When endpoint is empty it returns a no-op tracer
// and a no-op shutdown, so local runs without a collector work unchanged.
//
// The returned shutdown MUST be called on process exit to flush the batcher;
// otherwise the last spans never reach the collector.
func InitTracerProvider(ctx context.Context, endpoint, serviceName string) (shutdown func(context.Context) error, tracer trace.Tracer, err error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, noop.NewTracerProvider().Tracer(serviceName), nil
	}
	if serviceName == "" {
		serviceName = "aletheia-api"
	}

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("observability: otlp exporter: %w", err)
	}

	// Schemaless avoids schema-URL conflicts with the SDK's default resource;
	// service.name is the only attribute Jaeger needs to group the trace.
	res := resource.NewSchemaless(attribute.String("service.name", serviceName))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, tp.Tracer(serviceName), nil
}
