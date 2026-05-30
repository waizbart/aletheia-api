package observability

import "context"

// multiSink fans every recorder call out to several underlying recorders, so a
// single use case call feeds both the live dashboard collector and the
// OpenTelemetry bridge.
type multiSink struct {
	recs []Recorder
}

// NewMultiRecorder combines recorders. nil entries are dropped. With a single
// recorder it is returned as-is; with none, a no-op recorder is returned.
func NewMultiRecorder(recs ...Recorder) Recorder {
	filtered := make([]Recorder, 0, len(recs))
	for _, r := range recs {
		if r != nil {
			filtered = append(filtered, r)
		}
	}
	switch len(filtered) {
	case 0:
		return nopRecorder{}
	case 1:
		return filtered[0]
	default:
		return &multiSink{recs: filtered}
	}
}

func (m *multiSink) StartStage(ctx context.Context, name string) StageHandle {
	handles := make([]StageHandle, len(m.recs))
	for i, r := range m.recs {
		handles[i] = r.StartStage(ctx, name)
	}
	return &multiHandle{handles: handles}
}

func (m *multiSink) SetPipeline(kind string) {
	for _, r := range m.recs {
		r.SetPipeline(kind)
	}
}

func (m *multiSink) SetVerdict(v Verdict) {
	for _, r := range m.recs {
		r.SetVerdict(v)
	}
}

type multiHandle struct {
	handles []StageHandle
}

func (h *multiHandle) SetAttrs(attrs ...Attr) {
	for _, sh := range h.handles {
		sh.SetAttrs(attrs...)
	}
}

func (h *multiHandle) Fail(err error) {
	for _, sh := range h.handles {
		sh.Fail(err)
	}
}

func (h *multiHandle) Skip(reason string) {
	for _, sh := range h.handles {
		sh.Skip(reason)
	}
}

func (h *multiHandle) Child(name string) StageHandle {
	children := make([]StageHandle, len(h.handles))
	for i, sh := range h.handles {
		children[i] = sh.Child(name)
	}
	return &multiHandle{handles: children}
}

func (h *multiHandle) End() {
	for _, sh := range h.handles {
		sh.End()
	}
}
