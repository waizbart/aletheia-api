package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// otelSink turns stage events into OpenTelemetry spans. Each stage becomes a
// span; per-request it is created with the request span as parent so the whole
// pipeline nests under one trace.
type otelSink struct {
	tracer  trace.Tracer
	reqSpan trace.Span
}

func (s *otelSink) SetPipeline(kind string) {
	s.reqSpan.SetAttributes(attribute.String("pipeline", kind))
}

func (s *otelSink) SetVerdict(v Verdict) {
	s.reqSpan.SetAttributes(attribute.String("verdict.outcome", v.Outcome))
}

func (s *otelSink) StartStage(ctx context.Context, name string) StageHandle {
	spanCtx, span := s.tracer.Start(ctx, name)
	return &otelHandle{tracer: s.tracer, ctx: spanCtx, span: span}
}

type otelHandle struct {
	tracer trace.Tracer
	ctx    context.Context
	span   trace.Span
}

func (h *otelHandle) SetAttrs(attrs ...Attr) {
	h.span.SetAttributes(toKeyValues(attrs)...)
}

func (h *otelHandle) Fail(err error) {
	if err != nil {
		h.span.RecordError(err)
		h.span.SetStatus(codes.Error, err.Error())
	}
}

func (h *otelHandle) Skip(reason string) {
	h.span.SetAttributes(attribute.String("skipped", reason))
}

func (h *otelHandle) Child(name string) StageHandle {
	spanCtx, span := h.tracer.Start(h.ctx, name)
	return &otelHandle{tracer: h.tracer, ctx: spanCtx, span: span}
}

func (h *otelHandle) End() {
	h.span.End()
}

func toKeyValues(attrs []Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.Value.(type) {
		case string:
			out = append(out, attribute.String(a.Key, v))
		case bool:
			out = append(out, attribute.Bool(a.Key, v))
		case int:
			out = append(out, attribute.Int(a.Key, v))
		case int64:
			out = append(out, attribute.Int64(a.Key, v))
		case float64:
			out = append(out, attribute.Float64(a.Key, v))
		case float32:
			out = append(out, attribute.Float64(a.Key, float64(v)))
		default:
			// Fall back to a string rendering for anything else.
			out = append(out, attribute.String(a.Key, fmt.Sprintf("%v", v)))
		}
	}
	return out
}
