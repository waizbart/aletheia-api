//go:build integration

package feature_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// Fixtures are generated in process rather than committed. The project already
// builds its image fixtures that way, and a generated clip lets a re-encode be
// modelled exactly: the painter is a function of normalized time, so a clip with
// 50 frames and a clip with 60 frames show the same scene at the same instants,
// which is what an fps transcode actually does.
//
// MJPEG is present in every libavcodec build. H.264 usually is but is not
// something to bet a suite on, so every clip helper skips when the encoder is
// missing — VideoWriterFile reports that through IsOpened, not through an error.

// videoScene paints a frame at normalized time t into a BGR byte buffer.
//
// The background is a block grid in normalized coordinates: strong edges for
// ORB, low enough spatial frequency to survive both a rescale and lossy
// compression. A bright square moves with t so consecutive frames differ, which
// is what stops the sequence gate from being vacuous.
func videoScene(width, height int, t float64) []byte {
	const blocksX, blocksY = 16, 12
	buf := make([]byte, width*height*3)

	sqSize := 0.18
	sqX := 0.05 + t*(1-sqSize-0.1)
	sqY := 0.30 + 0.25*t

	for y := 0; y < height; y++ {
		v := (float64(y) + 0.5) / float64(height)
		for x := 0; x < width; x++ {
			u := (float64(x) + 0.5) / float64(width)

			bx := int(u * blocksX)
			by := int(v * blocksY)
			// A cheap deterministic hash of the block coordinates. Distinct
			// neighbouring values give ORB real corners to find.
			seed := (bx * 73856093) ^ (by * 19349663)
			b := byte(40 + (seed&0x7fffffff)%180)
			g := byte(40 + ((seed>>7)&0x7fffffff)%180)
			r := byte(40 + ((seed>>13)&0x7fffffff)%180)

			if u >= sqX && u < sqX+sqSize && v >= sqY && v < sqY+sqSize {
				b, g, r = 250, 250, 250
			}

			off := (y*width + x) * 3
			buf[off+0] = b
			buf[off+1] = g
			buf[off+2] = r
		}
	}
	return buf
}

// videoSceneShifted paints an unrelated scene, used as the negative control.
func videoSceneShifted(width, height int, t float64) []byte {
	const blocksX, blocksY = 9, 7
	buf := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		v := (float64(y) + 0.5) / float64(height)
		for x := 0; x < width; x++ {
			u := (float64(x) + 0.5) / float64(width)
			bx := int(u * blocksX)
			by := int(v * blocksY)
			seed := (bx * 2654435761) ^ (by * 2246822519) ^ 0x5bf03635
			buf[(y*width+x)*3+0] = byte(20 + (seed&0x7fffffff)%200)
			buf[(y*width+x)*3+1] = byte(20 + ((seed>>9)&0x7fffffff)%200)
			buf[(y*width+x)*3+2] = byte(20 + ((seed>>17)&0x7fffffff)%200)
		}
	}
	// A slow global brightening so consecutive frames are not identical.
	shift := byte(int(t*40) % 40)
	for i := range buf {
		if int(buf[i])+int(shift) < 255 {
			buf[i] += shift
		}
	}
	return buf
}

type clipSpec struct {
	name    string
	codec   string
	ext     string
	width   int
	height  int
	fps     float64
	frames  int
	painter func(w, h int, t float64) []byte
}

// writeClip renders a clip to a temp file, skipping the test when the requested
// encoder is unavailable.
func writeClip(t *testing.T, spec clipSpec) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), spec.name+spec.ext)
	writer, err := gocv.VideoWriterFile(path, spec.codec, spec.fps, spec.width, spec.height, true)
	if err != nil {
		t.Skipf("cannot open a %s writer: %v", spec.codec, err)
	}
	// VideoWriter_Open returns void, so an unavailable codec surfaces only here.
	if !writer.IsOpened() {
		writer.Close()
		t.Skipf("the %s encoder is not available in this OpenCV build", spec.codec)
	}

	for i := 0; i < spec.frames; i++ {
		tt := (float64(i) + 0.5) / float64(spec.frames)
		data := spec.painter(spec.width, spec.height, tt)
		mat, merr := gocv.NewMatFromBytes(spec.height, spec.width, gocv.MatTypeCV8UC3, data)
		if merr != nil {
			writer.Close()
			t.Fatalf("building frame %d: %v", i, merr)
		}
		if werr := writer.Write(mat); werr != nil {
			mat.Close()
			writer.Close()
			t.Fatalf("writing frame %d: %v", i, werr)
		}
		mat.Close()
	}
	writer.Close()

	if st, serr := os.Stat(path); serr != nil || st.Size() == 0 {
		t.Skipf("the %s encoder produced no output", spec.codec)
	}
	return path
}

func baseClip(name string) clipSpec {
	return clipSpec{
		name: name, codec: "MJPG", ext: ".avi",
		width: 640, height: 480, fps: 25, frames: 50, painter: videoScene,
	}
}

func newVideoExtractor(t *testing.T, maxBytes int64) *feature.VideoExtractor {
	t.Helper()
	return feature.NewVideoExtractor(feature.VideoLimits{
		MaxBytes:    maxBytes,
		TempDir:     t.TempDir(),
		Concurrency: 2,
	})
}

// ingestFile takes a clip in and returns the handle. The caller closes it.
func ingestFile(t *testing.T, ext *feature.VideoExtractor, path string) usecase.VideoHandle {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open clip: %v", err)
	}
	defer f.Close()

	h, err := ext.Ingest(context.Background(), f)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return h
}

func reduceFile(t *testing.T, ext *feature.VideoExtractor, path string) *usecase.VideoReduction {
	t.Helper()
	h := ingestFile(t, ext, path)
	defer h.Close()

	red, err := ext.Reduce(context.Background(), h)
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	return red
}

func TestVideoExtractor_IngestHashesAndProbes(t *testing.T) {
	path := writeClip(t, baseClip("probe"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}
	want := sha256.Sum256(raw)

	ext := newVideoExtractor(t, 64<<20)
	h := ingestFile(t, ext, path)

	// The hash is computed during the spill copy, so it must agree with hashing
	// the bytes directly or the duplicate check is looking up the wrong thing.
	if got := h.ContentHash(); got != hex.EncodeToString(want[:]) {
		t.Errorf("ContentHash() = %s, want %x", got, want)
	}

	probe := h.Probe()
	if probe.Width != 640 || probe.Height != 480 {
		t.Errorf("probe dimensions = %dx%d, want 640x480", probe.Width, probe.Height)
	}
	if probe.FrameCount != 50 {
		t.Errorf("probe FrameCount = %d, want 50", probe.FrameCount)
	}
	if probe.SizeBytes != int64(len(raw)) {
		t.Errorf("probe SizeBytes = %d, want %d", probe.SizeBytes, len(raw))
	}
	// 50 frames at 25fps is two seconds; container rounding is allowed to drift.
	if probe.DurationMs < 1900 || probe.DurationMs > 2100 {
		t.Errorf("probe DurationMs = %d, want about 2000", probe.DurationMs)
	}
	if err := probe.Validate(); err != nil {
		t.Errorf("probe failed policy validation: %v", err)
	}

	if err := h.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Both use cases close the handle on every path, including the ones that
	// also return an error, so a second Close must be harmless — and must not
	// release the decode slot twice.
	if err := h.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestVideoExtractor_IngestLeavesNoSpillFileBehind(t *testing.T) {
	tempDir := t.TempDir()
	ext := feature.NewVideoExtractor(feature.VideoLimits{MaxBytes: 64 << 20, TempDir: tempDir, Concurrency: 1})
	path := writeClip(t, baseClip("cleanup"))

	h := ingestFile(t, ext, path)
	entries, _ := os.ReadDir(tempDir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one spill file while the handle is open, found %d", len(entries))
	}

	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	entries, _ = os.ReadDir(tempDir)
	if len(entries) != 0 {
		t.Fatalf("spill file survived Close: %v", entries)
	}
}

func TestVideoExtractor_IngestRejectsOversizedUpload(t *testing.T) {
	tempDir := t.TempDir()
	ext := feature.NewVideoExtractor(feature.VideoLimits{MaxBytes: 1024, TempDir: tempDir, Concurrency: 1})

	_, err := ext.Ingest(context.Background(), bytes.NewReader(make([]byte, 4096)))
	if !errors.Is(err, domain.ErrVideoTooLarge) {
		t.Fatalf("error = %v, want ErrVideoTooLarge", err)
	}

	// A refused ingest must not leak the partial spill.
	if entries, _ := os.ReadDir(tempDir); len(entries) != 0 {
		t.Fatalf("a rejected upload left %d file(s) behind", len(entries))
	}
}

func TestVideoExtractor_IngestRejectsUndecodableBytes(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	_, err := ext.Ingest(context.Background(), bytes.NewReader([]byte("this is not a video container")))
	if !errors.Is(err, domain.ErrVideoUndecodable) {
		t.Fatalf("error = %v, want ErrVideoUndecodable", err)
	}
}

func TestVideoExtractor_ReduceProducesAFullSequenceAndUsableAnchor(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	red := reduceFile(t, ext, writeClip(t, baseClip("reduce")))

	if got := len(red.Signature.FramePHashes); got != domain.VideoFrameSamples {
		t.Fatalf("sampled %d frames, want %d", got, domain.VideoFrameSamples)
	}
	if !red.Signature.Valid() {
		t.Fatalf("signature is not valid: anchor %d of %d frames",
			red.Signature.AnchorIndex, len(red.Signature.FramePHashes))
	}
	// The stored anchor pHash and the anchor slot have to describe the same
	// frame, or verify matches the ORB signature of one frame against the pHash
	// of another.
	if red.AnchorPHash != red.Signature.FramePHashes[red.Signature.AnchorIndex] {
		t.Error("AnchorPHash does not match the frame at AnchorIndex")
	}
	if red.Anchor == nil || !red.Anchor.HasColorGrid() {
		t.Fatal("anchor signature is missing its color grid; the match gate reads it from there")
	}
	if red.Anchor.DescriptorCount() < domain.MinInliers {
		t.Errorf("anchor has %d descriptors, too few to ever reach MinInliers (%d)",
			red.Anchor.DescriptorCount(), domain.MinInliers)
	}
	if _, err := png.Decode(bytes.NewReader(red.AnchorFrame)); err != nil {
		t.Fatalf("anchor frame is not decodable PNG: %v", err)
	}
	if red.Signature.DurationMs < 1900 || red.Signature.DurationMs > 2100 {
		t.Errorf("signature DurationMs = %d, want about 2000", red.Signature.DurationMs)
	}
}

// Sampling and anchor choice must be pure functions of the decoded frames.
// Certify and verify run this independently, and any disagreement makes the
// certificate unverifiable.
func TestVideoExtractor_ReduceIsDeterministic(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	path := writeClip(t, baseClip("determinism"))

	first := reduceFile(t, ext, path)
	second := reduceFile(t, ext, path)

	if first.Signature.AnchorIndex != second.Signature.AnchorIndex {
		t.Errorf("anchor index differs between runs: %d vs %d",
			first.Signature.AnchorIndex, second.Signature.AnchorIndex)
	}
	if len(first.Signature.FramePHashes) != len(second.Signature.FramePHashes) {
		t.Fatalf("frame counts differ: %d vs %d",
			len(first.Signature.FramePHashes), len(second.Signature.FramePHashes))
	}
	for i := range first.Signature.FramePHashes {
		if first.Signature.FramePHashes[i] != second.Signature.FramePHashes[i] {
			t.Fatalf("frame %d hashed differently between runs", i)
		}
	}
}

// A clip shorter than the sample count maps several slots onto the same frame.
// That is correct and needs no special case, but it must not truncate or crash.
func TestVideoExtractor_ReduceShortVideo(t *testing.T) {
	spec := baseClip("short")
	spec.frames = 6
	spec.fps = 6

	ext := newVideoExtractor(t, 64<<20)
	red := reduceFile(t, ext, writeClip(t, spec))

	if got := len(red.Signature.FramePHashes); got != domain.VideoFrameSamples {
		t.Fatalf("sampled %d frames from a 6-frame clip, want %d", got, domain.VideoFrameSamples)
	}
	if !red.Signature.Valid() {
		t.Error("short clip produced an invalid signature")
	}
}

// The point of the whole feature: the same video, re-encoded, still matches.
func TestVideoExtractor_SurvivesReencoding(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	reference := reduceFile(t, ext, writeClip(t, baseClip("reference")))

	variants := []struct {
		name string
		spec clipSpec
	}{
		{
			name: "rescaled to 320x240",
			spec: clipSpec{name: "scaled", codec: "MJPG", ext: ".avi",
				width: 320, height: 240, fps: 25, frames: 50, painter: videoScene},
		},
		{
			name: "frame rate converted to 30fps",
			spec: clipSpec{name: "fps30", codec: "MJPG", ext: ".avi",
				width: 640, height: 480, fps: 30, frames: 60, painter: videoScene},
		},
		{
			name: "frame rate converted to 15fps",
			spec: clipSpec{name: "fps15", codec: "MJPG", ext: ".avi",
				width: 640, height: 480, fps: 15, frames: 30, painter: videoScene},
		},
		{
			name: "transcoded to H.264 in MP4",
			spec: clipSpec{name: "h264", codec: "avc1", ext: ".mp4",
				width: 640, height: 480, fps: 25, frames: 50, painter: videoScene},
		},
		{
			name: "rescaled and transcoded",
			spec: clipSpec{name: "h264small", codec: "avc1", ext: ".mp4",
				width: 480, height: 360, fps: 30, frames: 60, painter: videoScene},
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			candidate := reduceFile(t, ext, writeClip(t, v.spec))

			agreement, matched := domain.FrameSequenceScore(
				reference.Signature.FramePHashes, candidate.Signature.FramePHashes)
			t.Logf("agreement %.3f (%d/%d frames) against the %v gate",
				agreement, matched, len(candidate.Signature.FramePHashes), domain.MinFrameAgreement)

			if agreement < domain.MinFrameAgreement {
				t.Errorf("agreement %.3f is below the %v gate — a re-encode of the same video was rejected",
					agreement, domain.MinFrameAgreement)
			}
			if !domain.DurationWithinTolerance(
				reference.Signature.DurationMs, candidate.Signature.DurationMs) {
				t.Errorf("duration %dms vs %dms fell outside tolerance",
					reference.Signature.DurationMs, candidate.Signature.DurationMs)
			}
		})
	}
}

func TestVideoExtractor_RejectsAnUnrelatedVideo(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	reference := reduceFile(t, ext, writeClip(t, baseClip("ref-neg")))

	other := baseClip("unrelated")
	other.painter = videoSceneShifted
	candidate := reduceFile(t, ext, writeClip(t, other))

	agreement, matched := domain.FrameSequenceScore(
		reference.Signature.FramePHashes, candidate.Signature.FramePHashes)
	t.Logf("unrelated video scored %.3f (%d/%d frames)",
		agreement, matched, len(candidate.Signature.FramePHashes))

	if agreement >= domain.MinFrameAgreement {
		t.Errorf("an unrelated video reached %.3f, at or above the %v gate",
			agreement, domain.MinFrameAgreement)
	}
}

func TestVideoExtractor_SweepsStaleSpillFiles(t *testing.T) {
	tempDir := t.TempDir()

	stale := filepath.Join(tempDir, "aletheia-vid-stale.bin")
	if err := os.WriteFile(stale, []byte("orphan"), 0o600); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	// Two hours back, past staleTempAge.
	backdated := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, backdated, backdated); err != nil {
		t.Fatalf("backdate stale file: %v", err)
	}

	fresh := filepath.Join(tempDir, "aletheia-vid-fresh.bin")
	if err := os.WriteFile(fresh, []byte("in flight"), 0o600); err != nil {
		t.Fatalf("write fresh file: %v", err)
	}
	unrelated := filepath.Join(tempDir, "someone-elses-file.bin")
	if err := os.WriteFile(unrelated, []byte("not ours"), 0o600); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}

	feature.NewVideoExtractor(feature.VideoLimits{MaxBytes: 1 << 20, TempDir: tempDir, Concurrency: 1})

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a stale spill file survived the startup sweep")
	}
	// A file belonging to a live process in another container, or to another
	// program entirely, must be left alone.
	if _, err := os.Stat(fresh); err != nil {
		t.Error("the sweep removed a spill file that was still fresh")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Error("the sweep removed a file it does not own")
	}
}

func TestVideoExtractor_ReduceRejectsAForeignHandle(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	if _, err := ext.Reduce(context.Background(), foreignHandle{}); err == nil {
		t.Fatal("expected Reduce to refuse a handle it did not create")
	}
}

type foreignHandle struct{}

func (foreignHandle) ContentHash() string      { return "" }
func (foreignHandle) Probe() domain.VideoProbe { return domain.VideoProbe{} }
func (foreignHandle) Close() error             { return nil }

// TestVideoCalibration_FramePHashDistances reports, rather than gates, the
// Hamming distances the thresholds are tuned against. The measured numbers
// belong in the doc comments of MaxFramePHashDistance and MinFrameAgreement —
// that is how MaxColorMean and MinAreaCoverage were derived.
func TestVideoCalibration_FramePHashDistances(t *testing.T) {
	ext := newVideoExtractor(t, 64<<20)
	reference := reduceFile(t, ext, writeClip(t, baseClip("cal-ref")))
	ref := reference.Signature.FramePHashes

	report := func(label string, cand [][32]byte) {
		lo, hi, sum := 256, 0, 0
		for i := range cand {
			if i >= len(ref) {
				break
			}
			d := domain.Hamming256(ref[i], cand[i])
			sum += d
			if d < lo {
				lo = d
			}
			if d > hi {
				hi = d
			}
		}
		n := min(len(ref), len(cand))
		agreement, matched := domain.FrameSequenceScore(ref, cand)
		t.Logf("%-34s per-slot Hamming min=%3d max=%3d mean=%5.1f | agreement %.3f (%d/%d)",
			label, lo, hi, float64(sum)/float64(n), agreement, matched, len(cand))
	}

	report("same clip, re-decoded", reduceFile(t, ext, writeClip(t, baseClip("cal-same"))).Signature.FramePHashes)

	scaled := clipSpec{name: "cal-scaled", codec: "MJPG", ext: ".avi",
		width: 320, height: 240, fps: 25, frames: 50, painter: videoScene}
	report("rescaled to half size", reduceFile(t, ext, writeClip(t, scaled)).Signature.FramePHashes)

	fps30 := clipSpec{name: "cal-fps30", codec: "MJPG", ext: ".avi",
		width: 640, height: 480, fps: 30, frames: 60, painter: videoScene}
	report("frame rate 25 to 30", reduceFile(t, ext, writeClip(t, fps30)).Signature.FramePHashes)

	h264 := clipSpec{name: "cal-h264", codec: "avc1", ext: ".mp4",
		width: 640, height: 480, fps: 25, frames: 50, painter: videoScene}
	report("H.264 transcode", reduceFile(t, ext, writeClip(t, h264)).Signature.FramePHashes)

	other := baseClip("cal-other")
	other.painter = videoSceneShifted
	report("unrelated video (negative)", reduceFile(t, ext, writeClip(t, other)).Signature.FramePHashes)

	// Adjacent-frame distance is what the one-slot alignment slack has to cover.
	adj := 0
	for i := 1; i < len(ref); i++ {
		adj += domain.Hamming256(ref[i-1], ref[i])
	}
	t.Logf("%-34s mean=%5.1f", "adjacent slots within the reference", float64(adj)/float64(len(ref)-1))

	fmt.Fprintln(os.Stderr) // keep the block readable in -v output
}
