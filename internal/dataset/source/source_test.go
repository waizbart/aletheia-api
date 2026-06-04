package source_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/waizbart/aletheia-api/internal/dataset/source"
	"github.com/waizbart/aletheia-api/internal/testdata"
)

func TestLocal_List(t *testing.T) {
	dir := testdata.Curated("smoke-base")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("smoke-base dir not found: %v", err)
	}

	l := &source.Local{Dir: dir}
	refs, err := l.List(42, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("List returned empty slice")
	}
	for _, r := range refs {
		if r.ID == "" {
			t.Error("ref has empty ID")
		}
		if r.MIME != "image/jpeg" && r.MIME != "image/png" {
			t.Errorf("unexpected MIME %q for %q", r.MIME, r.ID)
		}
	}
}

func TestLocal_ListCapped(t *testing.T) {
	dir := testdata.Curated("smoke-base")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("smoke-base dir not found: %v", err)
	}

	l := &source.Local{Dir: dir}
	refs, err := l.List(42, 5)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) > 5 {
		t.Errorf("List returned %d refs, want <=5", len(refs))
	}
}

func TestLocal_Fetch(t *testing.T) {
	dir := testdata.Curated("smoke-base")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("smoke-base dir not found: %v", err)
	}

	l := &source.Local{Dir: dir}
	refs, err := l.List(42, 1)
	if err != nil || len(refs) == 0 {
		t.Skip("no refs available")
	}

	b, err := l.Fetch(refs[0])
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(b) == 0 {
		t.Error("Fetch returned empty bytes")
	}
}

func TestLocal_Deterministic(t *testing.T) {
	dir := testdata.Curated("smoke-base")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("smoke-base dir not found: %v", err)
	}

	l := &source.Local{Dir: dir}
	a, err := l.List(42, 10)
	if err != nil {
		t.Fatalf("first List: %v", err)
	}
	b, err := l.List(42, 10)
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("different lengths: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Errorf("ref[%d] not deterministic: %q vs %q", i, a[i].ID, b[i].ID)
		}
	}
}

func TestLocal_MissingDir(t *testing.T) {
	l := &source.Local{Dir: filepath.Join(t.TempDir(), "nonexistent")}
	_, err := l.List(42, 10)
	if err == nil {
		t.Error("expected error for missing dir, got nil")
	}
}
