package observability

import "context"

// Stage runs fn as an instrumented pipeline stage named name. The stage handle
// is passed to fn so it can attach attributes computed during execution. The
// stage duration and terminal status are handled automatically: on a non-nil
// error the stage is marked failed. The error is returned unchanged so callers
// keep their existing control flow.
func Stage[T any](ctx context.Context, name string, fn func(StageHandle) (T, error)) (T, error) {
	h := FromContext(ctx).StartStage(ctx, name)
	defer h.End()
	v, err := fn(h)
	if err != nil {
		h.Fail(err)
	}
	return v, err
}

// StageVoid is Stage for operations that return only an error.
func StageVoid(ctx context.Context, name string, fn func(StageHandle) error) error {
	h := FromContext(ctx).StartStage(ctx, name)
	defer h.End()
	err := fn(h)
	if err != nil {
		h.Fail(err)
	}
	return err
}
