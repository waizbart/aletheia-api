package manifest_test

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/dataset/manifest"
)

func sampleManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Metadata: manifest.Metadata{
			GeneratorVersion: manifest.GeneratorVersion,
			RunID:            "test-run-1",
			Seed:             42,
			DatasetSource:    "local",
			BaseCount:        2,
			VariantsPerBase:  1,
			SampleCount:      2,
			Thresholds:       manifest.ActiveThresholds(),
			CreatedAt:        time.Now().UTC().Truncate(time.Second),
		},
		Samples: []manifest.Sample{
			{
				ID: "img_001__jpeg_recompress__q70", BaseImageID: "img_001",
				TransformFamily: "jpeg_recompress",
				Params:          map[string]any{"quality": 70},
				ExpectedMatch:   true, Confidence: "high", Borderline: false,
				Rationale: "light recompression", MIME: "image/jpeg", SHA256: "abc123",
				OutputPath: "variants/img_001/jpeg_recompress_q70.jpg",
			},
			{
				ID: "img_001__different_image", BaseImageID: "img_001",
				TransformFamily: "different_image",
				Params:          map[string]any{},
				ExpectedMatch:   false, Confidence: "high", Borderline: false,
				Rationale: "peer image", MIME: "image/jpeg", SHA256: "def456",
				OutputPath: "variants/img_001/different_image.jpg", IsNegControl: true,
				PeerBaseID: "img_002",
			},
		},
	}
}

func TestWriteReadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	m := sampleManifest()
	if err := manifest.WriteJSON(path, m); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got, err := manifest.ReadJSON(path)
	if err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if got.Metadata.RunID != m.Metadata.RunID {
		t.Errorf("RunID mismatch: got %q want %q", got.Metadata.RunID, m.Metadata.RunID)
	}
	if len(got.Samples) != len(m.Samples) {
		t.Fatalf("sample count: got %d want %d", len(got.Samples), len(m.Samples))
	}
	if got.Samples[0].ID != m.Samples[0].ID {
		t.Errorf("sample[0].ID mismatch: got %q want %q", got.Samples[0].ID, m.Samples[0].ID)
	}
	if got.Metadata.Thresholds.MinInliers == 0 {
		t.Error("thresholds_snapshot.min_inliers should be non-zero")
	}
	if got.Metadata.Thresholds.MinAreaCoverage == 0 {
		t.Error("thresholds_snapshot.min_area_coverage should be non-zero")
	}
}

func TestWriteCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.csv")
	m := sampleManifest()
	if err := manifest.WriteCSV(path, m); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open csv: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	// header + 2 data rows
	if len(rows) != 3 {
		t.Fatalf("csv rows: got %d want 3", len(rows))
	}
	if rows[1][0] != m.Samples[0].ID {
		t.Errorf("csv row[1][0]: got %q want %q", rows[1][0], m.Samples[0].ID)
	}
}
