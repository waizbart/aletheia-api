package domain_test

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// distinctFrames builds n mutually distant frame hashes. The guard is part of
// the test: every sequence assertion below depends on unrelated frames sitting
// well past MaxFramePHashDistance, so a fixture that silently violated that
// would turn real failures into passes.
func distinctFrames(t *testing.T, n int) [][32]byte {
	t.Helper()

	out := make([][32]byte, n)
	for i := range out {
		out[i] = sha256.Sum256([]byte{byte(i), 0x5a})
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if d := domain.Hamming256(out[i], out[j]); d <= domain.MaxFramePHashDistance {
				t.Fatalf("fixture frames %d and %d are only %d bits apart, need > %d",
					i, j, d, domain.MaxFramePHashDistance)
			}
		}
	}
	return out
}

// flipBits returns h with its first n bits inverted, so the Hamming distance
// from h is exactly n.
func flipBits(h [32]byte, n int) [32]byte {
	out := h
	for i := 0; i < n; i++ {
		out[i/8] ^= 1 << (7 - i%8)
	}
	return out
}

func TestValidMediaKind(t *testing.T) {
	tests := []struct {
		name string
		kind domain.MediaKind
		want bool
	}{
		{"image", domain.MediaKindImage, true},
		{"video", domain.MediaKindVideo, true},
		{"empty", domain.MediaKind(""), false},
		{"unknown", domain.MediaKind("audio"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.ValidMediaKind(tt.kind); got != tt.want {
				t.Errorf("ValidMediaKind(%q) = %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

func TestVideoProbe_Validate(t *testing.T) {
	ok := domain.VideoProbe{DurationMs: 10_000, Width: 1920, Height: 1080, FrameCount: 300, FPS: 30}

	tests := []struct {
		name    string
		probe   domain.VideoProbe
		wantErr error
	}{
		{"accepts a well-formed probe", ok, nil},
		{
			name:    "rejects zero duration",
			probe:   domain.VideoProbe{DurationMs: 0, Width: 640, Height: 480},
			wantErr: domain.ErrVideoUndecodable,
		},
		{
			name:    "rejects negative duration",
			probe:   domain.VideoProbe{DurationMs: -1, Width: 640, Height: 480},
			wantErr: domain.ErrVideoUndecodable,
		},
		{
			name:    "rejects zero width",
			probe:   domain.VideoProbe{DurationMs: 1000, Width: 0, Height: 480},
			wantErr: domain.ErrVideoUndecodable,
		},
		{
			name:    "rejects zero height",
			probe:   domain.VideoProbe{DurationMs: 1000, Width: 640, Height: 0},
			wantErr: domain.ErrVideoUndecodable,
		},
		{
			name:    "rejects a video past the duration ceiling",
			probe:   domain.VideoProbe{DurationMs: domain.MaxVideoDurationMs + 1, Width: 640, Height: 480},
			wantErr: domain.ErrVideoTooLong,
		},
		{
			name:    "rejects a frame past the pixel ceiling",
			probe:   domain.VideoProbe{DurationMs: 1000, Width: 7680, Height: 4320},
			wantErr: domain.ErrVideoResolution,
		},
		{
			name:    "accepts exactly the duration ceiling",
			probe:   domain.VideoProbe{DurationMs: domain.MaxVideoDurationMs, Width: 640, Height: 480},
			wantErr: nil,
		},
		{
			name:    "accepts exactly the pixel ceiling",
			probe:   domain.VideoProbe{DurationMs: 1000, Width: 3840, Height: 2160},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.probe.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVideoSignature_FrameCountAndValid(t *testing.T) {
	frames := distinctFrames(t, 4)

	tests := []struct {
		name      string
		sig       *domain.VideoSignature
		wantCount int
		wantValid bool
	}{
		{"nil signature", nil, 0, false},
		{"no frames", &domain.VideoSignature{}, 0, false},
		{
			name:      "negative anchor",
			sig:       &domain.VideoSignature{FramePHashes: frames, AnchorIndex: -1},
			wantCount: 4,
		},
		{
			name:      "anchor past the end",
			sig:       &domain.VideoSignature{FramePHashes: frames, AnchorIndex: 4},
			wantCount: 4,
		},
		{
			name:      "well-formed",
			sig:       &domain.VideoSignature{FramePHashes: frames, AnchorIndex: 2},
			wantCount: 4,
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sig.FrameCount(); got != tt.wantCount {
				t.Errorf("FrameCount() = %d, want %d", got, tt.wantCount)
			}
			if got := tt.sig.Valid(); got != tt.wantValid {
				t.Errorf("Valid() = %v, want %v", got, tt.wantValid)
			}
		})
	}
}

func TestSampleFrameIndexes_EmptyVideo(t *testing.T) {
	if got := domain.SampleFrameIndexes(0); got != nil {
		t.Errorf("SampleFrameIndexes(0) = %v, want nil", got)
	}
	if got := domain.SampleFrameIndexes(-5); got != nil {
		t.Errorf("SampleFrameIndexes(-5) = %v, want nil", got)
	}
}

func TestSampleFrameIndexes_InRangeAndMonotonic(t *testing.T) {
	for _, frameCount := range []int{1, 2, 7, 31, 32, 33, 300, 3600, 100_000} {
		idx := domain.SampleFrameIndexes(frameCount)
		if len(idx) != domain.VideoFrameSamples {
			t.Fatalf("frameCount %d: got %d samples, want %d",
				frameCount, len(idx), domain.VideoFrameSamples)
		}
		for i, n := range idx {
			if n < 0 || n >= frameCount {
				t.Fatalf("frameCount %d: sample %d is frame %d, outside [0,%d)",
					frameCount, i, n, frameCount)
			}
			if i > 0 && n < idx[i-1] {
				t.Fatalf("frameCount %d: sample %d (%d) went backwards from %d",
					frameCount, i, n, idx[i-1])
			}
		}
	}
}

// Slot 0 must never be frame 0 on a video long enough to have a choice: that is
// the frame most likely to be a fade-in or an auto-exposure ramp, and avoiding
// it is the whole reason the sampler uses slot midpoints.
func TestSampleFrameIndexes_SkipsTheVeryFirstAndReachesTheEnd(t *testing.T) {
	idx := domain.SampleFrameIndexes(3600)

	if idx[0] == 0 {
		t.Error("slot 0 landed on frame 0; midpoint sampling should skip it")
	}
	last := idx[len(idx)-1]
	if last < 3600*9/10 {
		t.Errorf("last slot is frame %d of 3600; the final tenth is never sampled", last)
	}
}

// The property the whole design rests on: sampling by proportion of the frame
// count puts slot i at the same wall-clock instant regardless of frame rate, so
// a 30-to-25fps transcode still compares like with like.
func TestSampleFrameIndexes_FrameRateInvariant(t *testing.T) {
	at30 := domain.SampleFrameIndexes(300) // 10s at 30fps
	at25 := domain.SampleFrameIndexes(250) // the same 10s at 25fps

	for i := range at30 {
		pos30 := float64(at30[i]) / 300
		pos25 := float64(at25[i]) / 250
		if diff := pos30 - pos25; diff > 0.01 || diff < -0.01 {
			t.Errorf("slot %d: 30fps at %.4f of the video, 25fps at %.4f — drift %.4f",
				i, pos30, pos25, diff)
		}
	}
}

func TestSampleFrameIndexes_Deterministic(t *testing.T) {
	a := domain.SampleFrameIndexes(1234)
	b := domain.SampleFrameIndexes(1234)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("slot %d differs between calls: %d vs %d", i, a[i], b[i])
		}
	}
}

func TestEncodeDecodeFramePHashes(t *testing.T) {
	frames := distinctFrames(t, 5)

	encoded := domain.EncodeFramePHashes(frames)
	if len(encoded) != 5*32 {
		t.Fatalf("encoded length = %d, want %d", len(encoded), 5*32)
	}

	decoded := domain.DecodeFramePHashes(encoded)
	if len(decoded) != len(frames) {
		t.Fatalf("decoded %d frames, want %d", len(decoded), len(frames))
	}
	for i := range frames {
		if decoded[i] != frames[i] {
			t.Errorf("frame %d round-tripped to %x, want %x", i, decoded[i], frames[i])
		}
	}
}

func TestEncodeFramePHashes_Empty(t *testing.T) {
	if got := domain.EncodeFramePHashes(nil); len(got) != 0 {
		t.Errorf("EncodeFramePHashes(nil) = %x, want empty", got)
	}
}

func TestDecodeFramePHashes_ShortAndTrailingBytes(t *testing.T) {
	if got := domain.DecodeFramePHashes(nil); got != nil {
		t.Errorf("DecodeFramePHashes(nil) = %v, want nil", got)
	}
	if got := domain.DecodeFramePHashes(make([]byte, 31)); got != nil {
		t.Errorf("a partial hash decoded to %v, want nil", got)
	}

	// A trailing partial hash is dropped rather than reported, matching the
	// tolerance the certificate columns already apply to a malformed pHash blob.
	frames := distinctFrames(t, 2)
	raw := append(domain.EncodeFramePHashes(frames), 0x01, 0x02)
	if got := domain.DecodeFramePHashes(raw); len(got) != 2 {
		t.Errorf("decoded %d frames from a trailing-byte blob, want 2", len(got))
	}
}

func TestFrameSequenceScore(t *testing.T) {
	ref := distinctFrames(t, 8)

	nearMiss := make([][32]byte, len(ref))
	for i, h := range ref {
		nearMiss[i] = flipBits(h, domain.MaxFramePHashDistance)
	}
	tooFar := make([][32]byte, len(ref))
	for i, h := range ref {
		tooFar[i] = flipBits(h, domain.MaxFramePHashDistance+1)
	}

	// Shifted by exactly one slot: every candidate frame still sits inside the
	// ±1 window, so the sequence must survive the transcode drift it models.
	shiftedByOne := make([][32]byte, len(ref))
	copy(shiftedByOne, ref[1:])
	shiftedByOne[len(ref)-1] = ref[len(ref)-1]

	// Shifted by two: outside the window, which is how a trim is rejected.
	shiftedByTwo := make([][32]byte, len(ref))
	copy(shiftedByTwo, ref[2:])
	shiftedByTwo[len(ref)-2] = ref[len(ref)-1]
	shiftedByTwo[len(ref)-1] = ref[len(ref)-1]

	tests := []struct {
		name          string
		ref, cand     [][32]byte
		wantMatched   int
		wantAgreement float64
	}{
		{"identical", ref, ref, 8, 1},
		{"at the distance limit", ref, nearMiss, 8, 1},
		{"one bit past the limit", ref, tooFar, 0, 0},
		{"shifted by one slot", ref, shiftedByOne, 8, 1},
		{"unrelated frames", ref, distinctFrames(t, 8)[:0], 0, 0},
		{"empty reference", nil, ref, 0, 0},
		{"empty candidate", ref, nil, 0, 0},
		{"truncated candidate", ref, ref[:3], 3, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agreement, matched := domain.FrameSequenceScore(tt.ref, tt.cand)
			if matched != tt.wantMatched {
				t.Errorf("matched = %d, want %d", matched, tt.wantMatched)
			}
			if agreement != tt.wantAgreement {
				t.Errorf("agreement = %v, want %v", agreement, tt.wantAgreement)
			}
		})
	}

	t.Run("shifted by two slots falls below the gate", func(t *testing.T) {
		agreement, _ := domain.FrameSequenceScore(ref, shiftedByTwo)
		if agreement >= domain.MinFrameAgreement {
			t.Errorf("agreement = %v, want below the %v gate", agreement, domain.MinFrameAgreement)
		}
	})
}

// A locked-off shot makes every frame near-identical, so the sequence gate
// carries no information and passes for any candidate built from the same
// still. This is inherent, and the anchor's ORB match is what discriminates —
// the test records the property so nobody later mistakes it for a bug.
func TestFrameSequenceScore_StaticSceneIsUninformative(t *testing.T) {
	still := sha256.Sum256([]byte("locked-off shot"))
	ref := make([][32]byte, 8)
	cand := make([][32]byte, 8)
	for i := range ref {
		ref[i] = still
		cand[i] = still
	}

	agreement, matched := domain.FrameSequenceScore(ref, cand)
	if agreement != 1 || matched != 8 {
		t.Fatalf("agreement = %v, matched = %d; want a full match", agreement, matched)
	}
}

func TestDurationWithinTolerance(t *testing.T) {
	tests := []struct {
		name          string
		refMs, candMs int
		want          bool
	}{
		{"identical", 10_000, 10_000, true},
		{"inside the ratio, candidate shorter", 100_000, 96_000, true},
		{"inside the ratio, candidate longer", 100_000, 104_000, true},
		{"outside the ratio, candidate shorter", 100_000, 94_000, false},
		{"outside the ratio, candidate longer", 100_000, 106_000, false},
		{"short video uses the absolute floor", 2_000, 2_400, true},
		{"short video past the absolute floor", 2_000, 2_600, false},
		{"exactly on the floor", 2_000, 2_500, true},
		{"unknown reference duration fails closed", 0, 10_000, false},
		{"unknown candidate duration fails closed", 10_000, 0, false},
		{"negative reference fails closed", -1, 10_000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.DurationWithinTolerance(tt.refMs, tt.candMs); got != tt.want {
				t.Errorf("DurationWithinTolerance(%d, %d) = %v, want %v",
					tt.refMs, tt.candMs, got, tt.want)
			}
		})
	}
}

func TestDecideVideo(t *testing.T) {
	good := domain.MatchDecision{
		Matched:   true,
		Inliers:   domain.MinInliers,
		ColorMean: domain.MaxColorMean,
		Coverage:  domain.MinAreaCoverage,
	}
	withColorMax := func(v float64) domain.MatchDecision {
		d := good
		d.ColorMax = v
		return d
	}

	tests := []struct {
		name       string
		anchor     domain.MatchDecision
		agreement  float64
		durationOK bool
		want       bool
	}{
		{"every gate exactly at its limit", good, domain.MinFrameAgreement, true, true},
		{"too few inliers", domain.MatchDecision{Inliers: domain.MinInliers - 1, Coverage: 1}, 1, true, false},
		{
			name:       "colour mean above the ceiling",
			anchor:     domain.MatchDecision{Inliers: 40, ColorMean: domain.MaxColorMean + 0.1, Coverage: 1},
			agreement:  1,
			durationOK: true,
		},
		{
			name:       "coverage below the floor",
			anchor:     domain.MatchDecision{Inliers: 40, Coverage: domain.MinAreaCoverage - 0.01},
			agreement:  1,
			durationOK: true,
		},
		{"agreement below the gate", good, domain.MinFrameAgreement - 0.01, true, false},
		{"duration out of tolerance", good, 1, false, false},
		{"everything failed", domain.MatchDecision{}, 0, false, false},
		// The per-cell ceiling is deliberately not applied to video: it tracks
		// how far the two frame grids drifted apart rather than whether the
		// picture changed, and legitimate frame rate conversions measure past
		// 137 with every other gate comfortably satisfied.
		{"a spiking cell does not reject a video", withColorMax(200), 1, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.DecideVideo(tt.anchor, tt.agreement, tt.durationOK)
			if got != tt.want {
				t.Errorf("DecideVideo = %v, want %v", got, tt.want)
			}
		})
	}
}

// The image verdict keeps the per-cell ceiling. Loosening video must not
// loosen the path it was calibrated for.
func TestDecide_StillAppliesThePerCellCeiling(t *testing.T) {
	if domain.Decide(40, 1, domain.MaxCellDist+1, 1) {
		t.Error("the image verdict accepted a cell past MaxCellDist")
	}
}

func TestVideoFeatureCommitment_Deterministic(t *testing.T) {
	sig := &domain.VideoSignature{
		FramePHashes: distinctFrames(t, 4),
		AnchorIndex:  2,
		DurationMs:   12_345,
	}
	var phash [32]byte
	phash[0] = 0xaa

	a := domain.VideoFeatureCommitment(&phash, nil, sig)
	b := domain.VideoFeatureCommitment(&phash, nil, sig)
	if a != b {
		t.Fatalf("commitment is not deterministic: %x vs %x", a, b)
	}
	if a == ([32]byte{}) {
		t.Fatal("commitment must not be the zero value")
	}
}

func TestVideoFeatureCommitment_NilSignature(t *testing.T) {
	a := domain.VideoFeatureCommitment(nil, nil, nil)
	b := domain.VideoFeatureCommitment(nil, nil, nil)
	if a != b {
		t.Fatalf("nil commitment is not deterministic: %x vs %x", a, b)
	}
	if a == ([32]byte{}) {
		t.Fatal("nil commitment must not be the zero value")
	}
}

// Every value the certificate asserts about the video has to be inside the
// commitment. If any of these could change without moving the digest, an
// operator could rewrite it and the Merkle proof would still verify.
func TestVideoFeatureCommitment_CoversEveryAssertedField(t *testing.T) {
	frames := distinctFrames(t, 4)
	base := &domain.VideoSignature{FramePHashes: frames, AnchorIndex: 1, DurationMs: 9_000}
	var phash [32]byte

	baseline := domain.VideoFeatureCommitment(&phash, nil, base)

	otherFrames := make([][32]byte, len(frames))
	copy(otherFrames, frames)
	otherFrames[3] = flipBits(otherFrames[3], 1)

	variants := map[string]*domain.VideoSignature{
		"anchor index": {FramePHashes: frames, AnchorIndex: 2, DurationMs: 9_000},
		"duration":     {FramePHashes: frames, AnchorIndex: 1, DurationMs: 9_001},
		"frame count":  {FramePHashes: frames[:3], AnchorIndex: 1, DurationMs: 9_000},
		"frame hash":   {FramePHashes: otherFrames, AnchorIndex: 1, DurationMs: 9_000},
	}

	for name, v := range variants {
		t.Run(name, func(t *testing.T) {
			if domain.VideoFeatureCommitment(&phash, nil, v) == baseline {
				t.Errorf("commitment did not change when the %s changed", name)
			}
		})
	}

	t.Run("anchor signature", func(t *testing.T) {
		sig := &domain.FeatureSignature{Descriptors: []byte("d"), Keypoints: []byte("k")}
		if domain.VideoFeatureCommitment(&phash, sig, base) == baseline {
			t.Error("commitment did not change when the anchor signature changed")
		}
	})

	t.Run("anchor phash", func(t *testing.T) {
		other := phash
		other[0] = 0x01
		if domain.VideoFeatureCommitment(&other, nil, base) == baseline {
			t.Error("commitment did not change when the anchor pHash changed")
		}
	})
}

// The domain tag must keep a video commitment out of reach of an image one,
// even when the anchor frame's features are exactly an image certificate's.
func TestVideoFeatureCommitment_CannotCollideWithImageCommitment(t *testing.T) {
	var phash [32]byte
	phash[7] = 0x42
	sig := &domain.FeatureSignature{Descriptors: []byte{1, 2}, Keypoints: []byte{3}}

	video := domain.VideoFeatureCommitment(&phash, sig, &domain.VideoSignature{})
	image := domain.FeatureCommitment(&phash, sig)
	if video == image {
		t.Fatal("a video commitment equalled an image commitment for the same anchor features")
	}
}

func TestVideoFeatureCommitment_Layout(t *testing.T) {
	frames := distinctFrames(t, 3)
	v := &domain.VideoSignature{FramePHashes: frames, AnchorIndex: 1, DurationMs: 4_096}
	var phash [32]byte
	phash[1] = 0x11
	sig := &domain.FeatureSignature{Descriptors: []byte{0xde}, Keypoints: []byte{0xad}}

	var header [8]byte
	binary.BigEndian.PutUint16(header[0:2], 1)
	binary.BigEndian.PutUint16(header[2:4], 3)
	binary.BigEndian.PutUint32(header[4:8], 4_096)

	anchor := domain.FeatureCommitment(&phash, sig)
	framesDigest := sha256.Sum256(domain.EncodeFramePHashes(frames))

	h := sha256.New()
	h.Write([]byte("aletheia-video-commitment-v1"))
	h.Write(header[:])
	h.Write(anchor[:])
	h.Write(framesDigest[:])
	var want [32]byte
	copy(want[:], h.Sum(nil))

	if got := domain.VideoFeatureCommitment(&phash, sig, v); got != want {
		t.Fatalf("commitment layout mismatch:\n got = %x\nwant = %x", got, want)
	}
}

func TestCertificate_IsVideo(t *testing.T) {
	var nilCert *domain.Certificate
	if nilCert.IsVideo() {
		t.Error("a nil certificate must not report as video")
	}
	if (&domain.Certificate{MediaKind: domain.MediaKindImage}).IsVideo() {
		t.Error("an image certificate reported as video")
	}
	if !(&domain.Certificate{MediaKind: domain.MediaKindVideo}).IsVideo() {
		t.Error("a video certificate did not report as video")
	}
}
