package observability

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TraceStage is one recorded pipeline stage within a Trace. Times are
// milliseconds; StartMs is the offset from the trace start.
type TraceStage struct {
	Name     string        `json:"name"`
	Status   StageStatus   `json:"status"`
	StartMs  float64       `json:"startMs"`
	DurMs    float64       `json:"durMs"`
	Attrs    []Attr        `json:"attrs,omitempty"`
	Error    string        `json:"error,omitempty"`
	Children []*TraceStage `json:"children,omitempty"`

	startedAt time.Time
	ended     bool
}

// Trace is a full request-level pipeline capture.
type Trace struct {
	ID        string        `json:"id"`
	Pipeline  string        `json:"pipeline"`
	StartedAt time.Time     `json:"startedAt"`
	Stages    []*TraceStage `json:"stages"`
	Verdict   Verdict       `json:"verdict"`
	TotalMs   float64       `json:"totalMs"`
	Done      bool          `json:"done"`
}

// Event is a single SSE message describing a change to a trace.
type Event struct {
	Type     string      `json:"type"` // trace_start|stage_start|stage_update|stage_end|trace_end
	TraceID  string      `json:"traceId"`
	Pipeline string      `json:"pipeline,omitempty"`
	Stage    *TraceStage `json:"stage,omitempty"`
	Verdict  *Verdict    `json:"verdict,omitempty"`
	TotalMs  float64     `json:"totalMs,omitempty"`
}

const defaultCapacity = 50

// Collector keeps a ring buffer of recent traces and fans live events out to
// SSE subscribers. It is safe for concurrent use.
type Collector struct {
	mu        sync.RWMutex
	capacity  int
	ring      []*Trace
	subs      map[int]chan Event
	nextSubID int
	nextTrace int
}

// NewCollector returns a collector retaining up to capacity recent traces.
func NewCollector(capacity int) *Collector {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Collector{
		capacity: capacity,
		subs:     make(map[int]chan Event),
	}
}

// Recent returns deep copies of the retained traces, newest last.
func (c *Collector) Recent() []*Trace {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Trace, len(c.ring))
	for i, t := range c.ring {
		out[i] = copyTrace(t)
	}
	return out
}

// Get returns a deep copy of the trace with the given id, or nil.
func (c *Collector) Get(id string) *Trace {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.ring {
		if t.ID == id {
			return copyTrace(t)
		}
	}
	return nil
}

// Subscribe registers a live-event listener. The returned cancel func must be
// called to release the subscription.
func (c *Collector) Subscribe() (ch chan Event, cancel func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSubID
	c.nextSubID++
	ch = make(chan Event, 64)
	c.subs[id] = ch
	return ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if existing, ok := c.subs[id]; ok {
			delete(c.subs, id)
			close(existing)
		}
	}
}

// publish performs a non-blocking broadcast; slow subscribers drop the event
// rather than stalling the request goroutine.
func (c *Collector) publish(e Event) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, ch := range c.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (c *Collector) store(t *Trace) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ring = append(c.ring, t)
	if len(c.ring) > c.capacity {
		c.ring = c.ring[len(c.ring)-c.capacity:]
	}
}

// NewSink creates a fresh per-request recorder backed by this collector.
func (c *Collector) NewSink() *collectorSink {
	c.mu.Lock()
	c.nextTrace++
	id := fmt.Sprintf("t-%d", c.nextTrace)
	c.mu.Unlock()

	t := &Trace{ID: id, StartedAt: time.Now()}
	s := &collectorSink{coll: c, trace: t}
	c.publish(Event{Type: "trace_start", TraceID: t.ID})
	return s
}

// collectorSink is a single-request Recorder. The request handler is the only
// writer, so per-trace fields need no locking; the shared collector guards its
// own state.
type collectorSink struct {
	coll  *Collector
	trace *Trace
}

func (s *collectorSink) SetPipeline(kind string) {
	s.trace.Pipeline = kind
	s.coll.publish(Event{Type: "stage_update", TraceID: s.trace.ID, Pipeline: kind})
}

func (s *collectorSink) SetVerdict(v Verdict) {
	s.trace.Verdict = v
	s.trace.Done = true
	s.trace.TotalMs = msSince(s.trace.StartedAt)
	s.coll.store(s.trace)
	vc := v
	s.coll.publish(Event{
		Type:     "trace_end",
		TraceID:  s.trace.ID,
		Pipeline: s.trace.Pipeline,
		Verdict:  &vc,
		TotalMs:  s.trace.TotalMs,
	})
}

func (s *collectorSink) StartStage(_ context.Context, name string) StageHandle {
	st := &TraceStage{
		Name:      name,
		Status:    StatusOK,
		StartMs:   msSince(s.trace.StartedAt),
		startedAt: time.Now(),
	}
	s.trace.Stages = append(s.trace.Stages, st)
	h := &collectorHandle{sink: s, stage: st, root: st}
	h.publish("stage_start")
	return h
}

// collectorHandle tracks a single (possibly nested) stage. root is the
// top-level ancestor whose snapshot is broadcast on every change, so the UI
// always receives the complete stage subtree.
type collectorHandle struct {
	sink  *collectorSink
	stage *TraceStage
	root  *TraceStage
}

func (h *collectorHandle) SetAttrs(attrs ...Attr) {
	h.stage.Attrs = append(h.stage.Attrs, attrs...)
	h.publish("stage_update")
}

func (h *collectorHandle) Fail(err error) {
	h.stage.Status = StatusError
	if err != nil {
		h.stage.Error = err.Error()
	}
}

func (h *collectorHandle) Skip(reason string) {
	h.stage.Status = StatusSkipped
	if reason != "" {
		h.stage.Attrs = append(h.stage.Attrs, Attr{Key: "reason", Value: reason})
	}
}

func (h *collectorHandle) Child(name string) StageHandle {
	child := &TraceStage{
		Name:      name,
		Status:    StatusOK,
		StartMs:   msSince(h.sink.trace.StartedAt),
		startedAt: time.Now(),
	}
	h.stage.Children = append(h.stage.Children, child)
	ch := &collectorHandle{sink: h.sink, stage: child, root: h.root}
	ch.publish("stage_update")
	return ch
}

func (h *collectorHandle) End() {
	if h.stage.ended {
		return
	}
	h.stage.ended = true
	h.stage.DurMs = msSince(h.stage.startedAt)
	h.publish("stage_end")
}

func (h *collectorHandle) publish(typ string) {
	h.sink.coll.publish(Event{
		Type:     typ,
		TraceID:  h.sink.trace.ID,
		Pipeline: h.sink.trace.Pipeline,
		Stage:    copyStage(h.root),
	})
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}

func copyTrace(t *Trace) *Trace {
	cp := *t
	cp.Stages = make([]*TraceStage, len(t.Stages))
	for i, st := range t.Stages {
		cp.Stages[i] = copyStage(st)
	}
	return &cp
}

func copyStage(s *TraceStage) *TraceStage {
	cp := *s
	if len(s.Attrs) > 0 {
		cp.Attrs = append([]Attr(nil), s.Attrs...)
	}
	if len(s.Children) > 0 {
		cp.Children = make([]*TraceStage, len(s.Children))
		for i, c := range s.Children {
			cp.Children[i] = copyStage(c)
		}
	}
	return &cp
}
