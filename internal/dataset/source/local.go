package source

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Local is a Source that reads images from a local directory.
// It is used for offline/CI runs against testdata/curated/smoke-base.
type Local struct {
	Dir string
}

func (l *Local) List(_ int64, n int) ([]Ref, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return nil, fmt.Errorf("local source: read dir %q: %w", l.Dir, err)
	}

	var refs []Ref
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		var mime string
		switch {
		case strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg"):
			mime = "image/jpeg"
		case strings.HasSuffix(name, ".png"):
			mime = "image/png"
		default:
			continue
		}
		refs = append(refs, Ref{
			ID:   strings.TrimSuffix(name, filepath.Ext(name)),
			Path: filepath.Join(l.Dir, e.Name()),
			MIME: mime,
		})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	if n > 0 && len(refs) > n {
		refs = refs[:n]
	}
	return refs, nil
}

func (l *Local) Fetch(ref Ref) ([]byte, error) {
	b, err := os.ReadFile(ref.Path)
	if err != nil {
		return nil, fmt.Errorf("local source: read %q: %w", ref.Path, err)
	}
	return b, nil
}
