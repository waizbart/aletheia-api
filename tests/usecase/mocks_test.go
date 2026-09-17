package usecase_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

type mockRepo struct {
	saveFn                    func(ctx context.Context, cert *domain.Certificate) error
	findByHashFn              func(ctx context.Context, hash string) (*domain.Certificate, error)
	findCandidatesByPHashesFn func(ctx context.Context, phashes [][32]byte, mediaKind domain.MediaKind, maxDistance, topK int) ([]*domain.Certificate, error)
	deleteFn                  func(ctx context.Context, hash string) error
}

func (m *mockRepo) Save(ctx context.Context, cert *domain.Certificate) error {
	return m.saveFn(ctx, cert)
}

func (m *mockRepo) FindByHash(ctx context.Context, hash string) (*domain.Certificate, error) {
	return m.findByHashFn(ctx, hash)
}

func (m *mockRepo) FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, mediaKind domain.MediaKind, maxDistance, topK int) ([]*domain.Certificate, error) {
	if m.findCandidatesByPHashesFn == nil {
		return nil, nil
	}
	return m.findCandidatesByPHashesFn(ctx, phashes, mediaKind, maxDistance, topK)
}

func (m *mockRepo) Delete(ctx context.Context, hash string) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, hash)
}

// validColorGrid returns a well-formed color grid for signatures used in
// tests: correct byte length with an arbitrary fill.
func validColorGrid() []byte {
	grid := make([]byte, domain.ColorGridBytes)
	for i := range grid {
		grid[i] = byte(i % 251)
	}
	return grid
}

// signatureWithGrid builds a complete stored signature (descriptors,
// keypoints, color grid, reference dims) as certify would persist it.
func signatureWithGrid() *domain.FeatureSignature {
	return &domain.FeatureSignature{
		Descriptors: []byte{0x01},
		Keypoints:   []byte{0x02},
		ColorGrid:   validColorGrid(),
		RefWidth:    1024,
		RefHeight:   768,
	}
}

type mockExtractor struct {
	computeFn func(ctx context.Context, content []byte) (*domain.FeatureSignature, error)
	matchFn   func(ctx context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error)
}

func (m *mockExtractor) Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, error) {
	if m.computeFn == nil {
		return signatureWithGrid(), nil
	}
	return m.computeFn(ctx, content)
}

func (m *mockExtractor) Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error) {
	if m.matchFn == nil {
		return domain.MatchDecision{}, nil
	}
	return m.matchFn(ctx, refSig, candSig, candImage)
}

type mockRenderer struct {
	renderFn func(grid []byte, refWidth, refHeight int) ([]byte, error)
}

func (m *mockRenderer) RenderColorGridPNG(grid []byte, refWidth, refHeight int) ([]byte, error) {
	if m.renderFn == nil {
		return []byte("png"), nil
	}
	return m.renderFn(grid, refWidth, refHeight)
}

// --- video mocks ------------------------------------------------------------

// mockVideoHandle stands in for an ingested video. closes counts Close calls so
// tests can assert the handle is released exactly once on every path: leaking
// one leaks both a spill file and a decode slot.
type mockVideoHandle struct {
	hash     string
	probe    domain.VideoProbe
	closes   int
	closeErr error
}

func (m *mockVideoHandle) ContentHash() string      { return m.hash }
func (m *mockVideoHandle) Probe() domain.VideoProbe { return m.probe }
func (m *mockVideoHandle) Close() error {
	m.closes++
	return m.closeErr
}

type mockVideoExtractor struct {
	ingestFn  func(ctx context.Context, r io.Reader) (usecase.VideoHandle, error)
	reduceFn  func(ctx context.Context, h usecase.VideoHandle) (*usecase.VideoReduction, error)
	frameAtFn func(ctx context.Context, h usecase.VideoHandle, slot int) ([]byte, error)

	frameAtSlots []int
}

func (m *mockVideoExtractor) Ingest(ctx context.Context, r io.Reader) (usecase.VideoHandle, error) {
	if m.ingestFn == nil {
		return &mockVideoHandle{hash: "video-hash", probe: validVideoProbe()}, nil
	}
	return m.ingestFn(ctx, r)
}

func (m *mockVideoExtractor) Reduce(ctx context.Context, h usecase.VideoHandle) (*usecase.VideoReduction, error) {
	if m.reduceFn == nil {
		return validReduction(), nil
	}
	return m.reduceFn(ctx, h)
}

func (m *mockVideoExtractor) FrameAt(ctx context.Context, h usecase.VideoHandle, slot int) ([]byte, error) {
	m.frameAtSlots = append(m.frameAtSlots, slot)
	if m.frameAtFn == nil {
		return []byte("anchor-frame"), nil
	}
	return m.frameAtFn(ctx, h, slot)
}

type mockVideoCertifyRunner struct {
	executeFn func(ctx context.Context, in usecase.CertifyVideoInput) (*usecase.CertifyOutput, error)
	calls     int
	lastInput usecase.CertifyVideoInput
}

func (m *mockVideoCertifyRunner) Execute(ctx context.Context, in usecase.CertifyVideoInput) (*usecase.CertifyOutput, error) {
	m.calls++
	m.lastInput = in
	if m.executeFn == nil {
		return &usecase.CertifyOutput{Certificate: &domain.Certificate{
			ID:          "video-cert",
			ContentHash: in.Handle.ContentHash(),
			MediaKind:   domain.MediaKindVideo,
		}}, nil
	}
	return m.executeFn(ctx, in)
}

func validVideoProbe() domain.VideoProbe {
	return domain.VideoProbe{
		DurationMs: 8_000, Width: 1280, Height: 720,
		FrameCount: 240, FPS: 30, SizeBytes: 1 << 20,
	}
}

// videoFrames builds a deterministic frame sequence whose members sit far
// enough apart that a shifted or unrelated sequence cannot match by accident.
func videoFrames(seed byte, n int) [][32]byte {
	out := make([][32]byte, n)
	for i := range out {
		out[i] = sha256.Sum256([]byte{seed, byte(i)})
	}
	return out
}

func validReduction() *usecase.VideoReduction {
	frames := videoFrames(1, domain.VideoFrameSamples)
	return &usecase.VideoReduction{
		Signature: &domain.VideoSignature{
			FramePHashes: frames,
			AnchorIndex:  4,
			DurationMs:   8_000,
		},
		Anchor:      signatureWithGrid(),
		AnchorPHash: frames[4],
		AnchorFrame: []byte("anchor-frame"),
	}
}
