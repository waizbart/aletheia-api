package observability

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// Factory builds a fresh per-request Recorder that fans out to the live
// dashboard collector and (when configured) the OpenTelemetry tracer.
type Factory struct {
	collector *Collector
	tracer    trace.Tracer
}

// NewFactory wires the collector and tracer. Either may be nil.
func NewFactory(c *Collector, t trace.Tracer) *Factory {
	return &Factory{collector: c, tracer: t}
}

// NewRequest mints a request-level trace plus (optionally) a parent OTel span.
// It returns a context carrying both the recorder and the span, the recorder
// itself, and an end func that closes the request span. Callers must invoke end
// when the request finishes.
func (f *Factory) NewRequest(ctx context.Context, name string) (context.Context, Recorder, func()) {
	var sinks []Recorder
	end := func() {}

	if f.tracer != nil {
		spanCtx, span := f.tracer.Start(ctx, name)
		ctx = spanCtx
		sinks = append(sinks, &otelSink{tracer: f.tracer, reqSpan: span})
		end = func() { span.End() }
	}
	if f.collector != nil {
		sinks = append(sinks, f.collector.NewSink())
	}

	rec := NewMultiRecorder(sinks...)
	return WithRecorder(ctx, rec), rec, end
}
