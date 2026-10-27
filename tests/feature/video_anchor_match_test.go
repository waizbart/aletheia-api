//go:build integration

package feature_test

import (
	"context"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
)

// The anchor frame is the discriminator: the frame sequence says "this is the
// same timeline" and the anchor match says "and it is the same picture". If the
// anchor cannot match across a re-encode, nothing verifies, so this measures
// the gates directly rather than through a verdict.
func TestVideoExtractor_AnchorFrameMatchesAcrossReencoding(t *testing.T) {
	images := feature.NewOpenCVExtractor()
	defer images.Close()

	videos := newVideoExtractor(t, 64<<20)
	reference := reduceFile(t, videos, writeClip(t, baseClip("anchor-ref")))

	variants := []struct {
		name string
		spec clipSpec
	}{
		{"same size, re-encoded", baseClip("anchor-same")},
		{"rescaled to 320x240", clipSpec{name: "anchor-small", codec: "MJPG", ext: ".avi",
			width: 320, height: 240, fps: 25, frames: 50, painter: videoScene}},
		{"rescaled to 1280x960", clipSpec{name: "anchor-big", codec: "MJPG", ext: ".avi",
			width: 1280, height: 960, fps: 25, frames: 50, painter: videoScene}},
		{"frame rate 25 to 30", clipSpec{name: "anchor-fps30", codec: "MJPG", ext: ".avi",
			width: 640, height: 480, fps: 30, frames: 60, painter: videoScene}},
		{"frame rate 25 to 15", clipSpec{name: "anchor-fps15", codec: "MJPG", ext: ".avi",
			width: 640, height: 480, fps: 15, frames: 30, painter: videoScene}},
		{"frame rate 25 to 24", clipSpec{name: "anchor-fps24", codec: "MJPG", ext: ".avi",
			width: 640, height: 480, fps: 24, frames: 48, painter: videoScene}},
		{"H.264 transcode", clipSpec{name: "anchor-h264", codec: "avc1", ext: ".mp4",
			width: 640, height: 480, fps: 25, frames: 50, painter: videoScene}},
		{"rescaled and frame rate converted", clipSpec{name: "anchor-both", codec: "avc1", ext: ".mp4",
			width: 480, height: 360, fps: 30, frames: 60, painter: videoScene}},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			path := writeClip(t, v.spec)

			handle := ingestFile(t, videos, path)
			defer handle.Close()

			// The slot compared is the reference's, exactly as verify does it.
			frame, err := videos.FrameAt(context.Background(), handle, reference.Signature.AnchorIndex)
			if err != nil {
				t.Fatalf("FrameAt: %v", err)
			}

			candSig, err := images.Compute(context.Background(), frame)
			if err != nil {
				t.Fatalf("Compute on the anchor frame: %v", err)
			}

			decision, err := images.Match(context.Background(), reference.Anchor, candSig, frame)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}

			t.Logf("inliers=%d (min %d) colorMean=%.2f (max %.1f) colorMax=%.1f (max %.1f) coverage=%.2f (min %.2f) matched=%v",
				decision.Inliers, domain.MinInliers,
				decision.ColorMean, domain.MaxColorMean,
				decision.ColorMax, domain.MaxCellDist,
				decision.Coverage, domain.MinAreaCoverage,
				decision.Matched)

			// DecideVideo is what the verdict actually uses, and it omits the
			// per-cell ceiling on purpose — the numbers logged above are why.
			// Asserting through it keeps this test honest about the real gate
			// rather than about MatchDecision.Matched.
			if !domain.DecideVideo(decision, 1, true) {
				t.Errorf("the anchor frame of a legitimate re-encode failed the video gates")
			}
		})
	}
}
