package observability

import (
	"context"
	"errors"
	"testing"
)

func TestFromContext_NoRecorderReturnsNop(t *testing.T) {
	rec := FromContext(context.Background())
	if rec == nil {
		t.Fatal("FromContext returned nil; must always return a usable recorder")
	}
	// None of these must panic on the no-op recorder.
	rec.SetPipeline("certify")
	h := rec.StartStage(context.Background(), "x")
	h.SetAttrs(Attr{Key: "k", Value: 1})
	h.Skip("because")
	child := h.Child("c")
	child.End()
	h.Fail(errors.New("boom"))
	h.End()
	rec.SetVerdict(Verdict{Outcome: "error"})
}

func TestStageHelpers_RunFnAndPropagateError(t *testing.T) {
	ctx := context.Background()

	got, err := Stage(ctx, "s", func(StageHandle) (int, error) { return 42, nil })
	if err != nil || got != 42 {
		t.Fatalf("Stage: got (%d,%v), want (42,nil)", got, err)
	}

	wantErr := errors.New("fail")
	_, err = Stage(ctx, "s", func(StageHandle) (int, error) { return 0, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("Stage: want propagated error, got %v", err)
	}

	ran := false
	if err := StageVoid(ctx, "v", func(StageHandle) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("StageVoid: ran=%v err=%v", ran, err)
	}
}
