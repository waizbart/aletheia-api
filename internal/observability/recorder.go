// Package observability provides a lightweight, dependency-free instrumentation
// port for the application use cases plus the infrastructure that turns the
// emitted stage events into a live dashboard feed and OpenTelemetry spans.
//
// The use case layer depends ONLY on the Recorder/StageHandle interfaces here;
// it never imports OpenTelemetry or the HTTP layer. A no-op recorder is used
// whenever none is present in the context, so code (and tests) that never wire
// a recorder keep working unchanged.
package observability

import "context"

// StageStatus is the terminal state of an instrumented pipeline stage.
type StageStatus string

const (
	StatusOK      StageStatus = "ok"
	StatusError   StageStatus = "error"
	StatusSkipped StageStatus = "skipped"
)

// Attr is an ordered key→value pair attached to a stage or trace. Value must be
// a JSON-serializable scalar (string, int, int64, float64, bool) so that both
// the JSON dashboard feed and the OpenTelemetry attribute mapping can consume
// it without reflection surprises.
type Attr struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// Verdict is the overall outcome of a request-level trace.
type Verdict struct {
	// Outcome is one of: "certified", "duplicate", "match", "no_match", "error".
	Outcome string         `json:"outcome"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Recorder is the single abstraction the use case layer knows about. All
// methods must be safe to call on a no-op implementation and from a single
// goroutine per request.
type Recorder interface {
	// StartStage opens a stage under the current request trace. The returned
	// handle must be ended exactly once (the Stage helpers guarantee this).
	StartStage(ctx context.Context, name string) StageHandle
	// SetPipeline tags the trace kind ("certify" or "verify").
	SetPipeline(kind string)
	// SetVerdict records the overall request outcome.
	SetVerdict(v Verdict)
}

// StageHandle represents a single in-flight stage.
type StageHandle interface {
	// SetAttrs attaches one or more attributes to the stage.
	SetAttrs(attrs ...Attr)
	// Fail marks the stage as errored and records the error.
	Fail(err error)
	// Skip marks the stage as skipped with a human-readable reason.
	Skip(reason string)
	// Child opens a nested stage (used for per-candidate matching).
	Child(name string) StageHandle
	// End stamps the stage duration. Status defaults to StatusOK unless Fail or
	// Skip was called. Calling End more than once is a no-op.
	End()
}

// nopRecorder is the zero-cost recorder returned when none is in context.
type nopRecorder struct{}

func (nopRecorder) StartStage(context.Context, string) StageHandle { return nopHandle{} }
func (nopRecorder) SetPipeline(string)                             {}
func (nopRecorder) SetVerdict(Verdict)                             {}

type nopHandle struct{}

func (nopHandle) SetAttrs(...Attr)         {}
func (nopHandle) Fail(error)               {}
func (nopHandle) Skip(string)              {}
func (nopHandle) Child(string) StageHandle { return nopHandle{} }
func (nopHandle) End()                     {}
