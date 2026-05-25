//go:build integration

package feature_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/feature"
)

const testdataDir = "../../lab/hashing/testdata"
const referenceFile = "aletheia.jpg"

var expected = map[string]bool{
	"aletheia.jpg":             true,
	"aletheia.png":             true,
	"aletheia.gif":             true,
	"aletheia-q10.jpg":         true,
	"aletheia-q20.jpg":         true,
	"aletheia-q30.jpg":         true,
	"aletheia-q40.jpg":         true,
	"aletheia-q50.jpg":         true,
	"aletheia-q60.jpg":         true,
	"aletheia-q70.jpg":         true,
	"aletheia-q80.jpg":         true,
	"aletheia-rotated-90.jpg":  true,
	"aletheia-rotated-180.jpg": true,
	"aletheia-rotated-270.jpg": true,
	"aletheia-cropped-10p.jpg": true,
	"aletheia-changed-1.jpg":   false,
	"aletheia-changed-2.jpg":   false,
	"aletheia-changed-3.jpg":   false,
	"aletheia-changed-4.jpg":   false,
	"aletheia-changed-5.jpg":   false,
	"aletheia-changed-6.jpg":   false,
	"aletheia-changed-7.jpg":   false,
	"aletheia-filter-1.jpg":    false,
	"aletheia-filter-2.jpg":    false,
	"aletheia-filter-3.jpg":    false,
}

func noisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Intn(256))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestOpenCVExtractor_TinyImageGuard locks in the fix for the SIGSEGV that
// degenerate uploads triggered inside cgo: images below the ORB minimum must be
// rejected with a clean error, and images at/above it must run without crashing
// the test process.
func TestOpenCVExtractor_TinyImageGuard(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	// 1x1 is the original crash repro; 62 is just under the threshold.
	for _, size := range []int{1, 16, 62} {
		t.Run(fmt.Sprintf("%dpx_rejected", size), func(t *testing.T) {
			_, _, err := ext.Compute(ctx, noisePNG(t, size, size))
			if err == nil {
				t.Fatalf("expected error for %dx%d image, got nil", size, size)
			}
			if !strings.Contains(err.Error(), "too small") {
				t.Fatalf("error = %v, want 'too small'", err)
			}
		})
	}

	// At/above the threshold ORB must not crash. A non-crash outcome is either a
	// signature or a clean "no features detected" error.
	for _, size := range []int{63, 80} {
		t.Run(fmt.Sprintf("%dpx_no_crash", size), func(t *testing.T) {
			_, _, err := ext.Compute(ctx, noisePNG(t, size, size))
			if err != nil && !strings.Contains(err.Error(), "no features detected") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOpenCVExtractor_TestdataMatrix(t *testing.T) {
	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	refBytes, err := os.ReadFile(filepath.Join(testdataDir, referenceFile))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	refSig, refImage, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute reference: %v", err)
	}

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	passes, total := 0, 0
	for _, name := range names {
		want, ok := expected[name]
		if !ok {
			continue
		}
		total++

		t.Run(name, func(t *testing.T) {
			candBytes, err := os.ReadFile(filepath.Join(testdataDir, name))
			if err != nil {
				t.Fatalf("read candidate: %v", err)
			}
			candSig, _, err := ext.Compute(ctx, candBytes)
			if err != nil {
				t.Fatalf("compute candidate: %v", err)
			}

			decision, err := ext.Match(ctx, refSig, candSig, refImage, candBytes)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if decision.Matched != want {
				t.Errorf("matched = %v, want %v (inliers=%d, mean=%.2f, max=%.2f)",
					decision.Matched, want, decision.Inliers, decision.ColorMean, decision.ColorMax)
				return
			}
			passes++
		})
	}

	t.Logf("passes: %d/%d", passes, total)
}
