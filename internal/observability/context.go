package observability

import "context"

type recorderKey struct{}

// WithRecorder returns a context carrying the given recorder.
func WithRecorder(ctx context.Context, r Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, r)
}

// FromContext returns the recorder stored in ctx, or a no-op recorder when none
// is present. It NEVER returns nil, so callers can use the result directly.
func FromContext(ctx context.Context) Recorder {
	if r, ok := ctx.Value(recorderKey{}).(Recorder); ok && r != nil {
		return r
	}
	return nopRecorder{}
}
