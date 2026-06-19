// Package manifest provides schema structs and writers for the dataset ground-truth manifest.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

const GeneratorVersion = "1.0.0"

// ThresholdsSnapshot captures the active decision thresholds at manifest
// generation time so future readers can detect label drift.
type ThresholdsSnapshot struct {
	MinInliers       int     `json:"min_inliers"`
	MaxColorMean     float64 `json:"max_color_mean"`
	MaxCellDist      float64 `json:"max_cell_dist"`
	MinAreaCoverage  float64 `json:"min_area_coverage"`
	MaxPHashDistance int     `json:"max_phash_distance"`
}

// ActiveThresholds returns the snapshot from the live domain constants.
func ActiveThresholds() ThresholdsSnapshot {
	return ThresholdsSnapshot{
		MinInliers:       domain.MinInliers,
		MaxColorMean:     domain.MaxColorMean,
		MaxCellDist:      domain.MaxCellDist,
		MinAreaCoverage:  domain.MinAreaCoverage,
		MaxPHashDistance: domain.MaxPHashDistance,
	}
}

// Metadata is the top-level manifest header.
type Metadata struct {
	GeneratorVersion  string             `json:"generator_version"`
	RunID             string             `json:"run_id"`
	Seed              int64              `json:"seed"`
	DatasetSource     string             `json:"dataset_source"`
	SourceAttribution string             `json:"source_attribution,omitempty"`
	BaseCount         int                `json:"base_count"`
	VariantsPerBase   int                `json:"variants_per_base"`
	SampleCount       int                `json:"sample_count"`
	Thresholds        ThresholdsSnapshot `json:"thresholds_snapshot"`
	CreatedAt         time.Time          `json:"created_at"`
	ToolCommit        string             `json:"tool_commit,omitempty"`
}

// Sample is one generated image with its ground-truth label.
type Sample struct {
	ID              string         `json:"id"`
	BaseImageID     string         `json:"base_image_id"`
	SourcePath      string         `json:"source_path"`
	OutputPath      string         `json:"output_path"`
	TransformFamily string         `json:"transform_family"`
	Params          map[string]any `json:"params"`
	ExpectedMatch   bool           `json:"expected_match"`
	Confidence      string         `json:"confidence"`
	Borderline      bool           `json:"borderline"`
	Rationale       string         `json:"rationale"`
	MIME            string         `json:"mime"`
	SHA256          string         `json:"sha256"`
	IsNegControl    bool           `json:"is_negative_control"`
	PeerBaseID      string         `json:"peer_base_id,omitempty"`
}

// Manifest is the complete ground-truth manifest written to manifest.json.
type Manifest struct {
	Metadata Metadata `json:"metadata"`
	Samples  []Sample `json:"samples"`
}

// WriteJSON writes the manifest to path as indented JSON.
func WriteJSON(path string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("manifest: write %q: %w", path, err)
	}
	return nil
}

// ReadJSON reads a manifest from path.
func ReadJSON(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %q: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest: parse %q: %w", path, err)
	}
	return &m, nil
}
