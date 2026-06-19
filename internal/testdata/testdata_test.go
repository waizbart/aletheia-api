package testdata_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/waizbart/aletheia-api/internal/testdata"
)

func TestRoot(t *testing.T) {
	root, err := testdata.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("go.mod not found at Root %q: %v", root, err)
	}
}

func TestCurated(t *testing.T) {
	p := testdata.Curated("aletheia", "aletheia.jpg")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("Curated(aletheia/aletheia.jpg) = %q, stat: %v", p, err)
	}
}

func TestGenerated(t *testing.T) {
	p := testdata.Generated()
	// Generated dir may or may not exist yet; just check it has the right suffix.
	root, err := testdata.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join(root, "testdata", "generated")
	if p != want {
		t.Errorf("Generated() = %q, want %q", p, want)
	}
}

func TestManifestPathEnvOverride(t *testing.T) {
	t.Setenv("ALETHEIA_DATASET_MANIFEST", "/tmp/custom-manifest.json")
	if got := testdata.ManifestPath(); got != "/tmp/custom-manifest.json" {
		t.Errorf("ManifestPath() = %q, want override", got)
	}
}
