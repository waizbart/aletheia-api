package observability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// runSink drives a minimal certify-like lifecycle on a sink.
func runSink(s *collectorSink) {
	s.SetPipeline("certify")
	h := s.StartStage(context.Background(), "sha256")
	h.SetAttrs(Attr{Key: "content_hash", Value: "abc"})
	h.End()
	s.SetVerdict(Verdict{Outcome: "certified"})
}

func TestCollector_RingEviction(t *testing.T) {
	c := NewCollector(2)
	for i := 0; i < 5; i++ {
		runSink(c.NewSink())
	}
	recent := c.Recent()
	if len(recent) != 2 {
		t.Fatalf("ring should retain 2 traces, got %d", len(recent))
	}
	// Newest kept: t-4 and t-5.
	if recent[0].ID != "t-4" || recent[1].ID != "t-5" {
		t.Fatalf("unexpected retained IDs: %s, %s", recent[0].ID, recent[1].ID)
	}
	last := recent[1]
	if last.Pipeline != "certify" || last.Verdict.Outcome != "certified" || !last.Done {
		t.Fatalf("trace not finalized correctly: %+v", last)
	}
	if len(last.Stages) != 1 || last.Stages[0].Name != "sha256" || last.Stages[0].Status != StatusOK {
		t.Fatalf("unexpected stages: %+v", last.Stages)
	}
}

func TestCollector_GetByID(t *testing.T) {
	c := NewCollector(10)
	runSink(c.NewSink())
	if got := c.Get("t-1"); got == nil || got.ID != "t-1" {
		t.Fatalf("Get(t-1) = %v", got)
	}
	if got := c.Get("nope"); got != nil {
		t.Fatalf("Get(nope) should be nil, got %v", got)
	}
}

func TestCollector_PublishesLifecycleEvents(t *testing.T) {
	c := NewCollector(10)
	ch, cancel := c.Subscribe()
	defer cancel()

	runSink(c.NewSink())

	var types []string
	for len(ch) > 0 {
		types = append(types, (<-ch).Type)
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"trace_start", "stage_start", "stage_end", "trace_end"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected event %q in stream, got [%s]", want, joined)
		}
	}
}

func TestEvent_JSONSchema(t *testing.T) {
	e := Event{
		Type:     "stage_end",
		TraceID:  "t-1",
		Pipeline: "verify",
		Stage: &TraceStage{
			Name:   "candidate_matching",
			Status: StatusOK,
			Attrs:  []Attr{{Key: "candidates", Value: 3}},
			Children: []*TraceStage{
				{Name: "candidate", Status: StatusOK, Attrs: []Attr{{Key: "matched", Value: true}}},
			},
		},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"type":"stage_end"`, `"traceId":"t-1"`, `"children"`, `"key":"matched"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("event JSON missing %q: %s", want, s)
		}
	}
}

func TestCollector_Subscribe_NonBlocking(t *testing.T) {
	// A subscriber that never drains must not block publishers.
	c := NewCollector(10)
	_, cancel := c.Subscribe()
	defer cancel()
	for i := 0; i < 200; i++ {
		runSink(c.NewSink()) // would deadlock if publish blocked on the full channel
	}
}
