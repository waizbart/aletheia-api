// Package testdata provides path helpers for shared test fixtures.
// It works from any working directory (tests, lab tools, CLI commands)
// by walking up the directory tree to the repo root (where go.mod lives).
package testdata

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Root returns the absolute path to the repository root (the directory
// containing go.mod). It is located by walking up from the caller's
// source file, which is stable regardless of how the binary is invoked.
func Root() (string, error) {
	// Use the source location of this file as the anchor.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("testdata: runtime.Caller failed")
	}
	// This file lives at <root>/internal/testdata/testdata.go, so walk up two dirs.
	dir := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("testdata: go.mod not found at %s: %w", dir, err)
	}
	return dir, nil
}

// Curated returns the absolute path to a file or directory inside
// testdata/curated/. These files are committed to the repository and
// are always available without any setup.
func Curated(parts ...string) string {
	root, err := Root()
	if err != nil {
		panic(err)
	}
	parts = append([]string{root, "testdata", "curated"}, parts...)
	return filepath.Join(parts...)
}

// Generated returns the absolute path to a file or directory inside
// testdata/generated/. This directory is git-ignored; its contents are
// produced by running cmd/datasetgen.
func Generated(parts ...string) string {
	root, err := Root()
	if err != nil {
		panic(err)
	}
	parts = append([]string{root, "testdata", "generated"}, parts...)
	return filepath.Join(parts...)
}

// ManifestPath returns the path to manifest.json. It respects the
// ALETHEIA_DATASET_MANIFEST environment variable so CI or local runs can
// point at an alternative manifest.
func ManifestPath() string {
	if v := os.Getenv("ALETHEIA_DATASET_MANIFEST"); v != "" {
		return v
	}
	return Generated("manifest.json")
}
