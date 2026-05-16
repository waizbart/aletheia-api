//go:build integration

package feature_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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
