package domain_test

import (
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestDecide(t *testing.T) {
	const fullCover = 1.0
	tests := []struct {
		name      string
		inliers   int
		colorMean float64
		colorMax  float64
		coverage  float64
		want      bool
	}{
		{"all pass", 100, 1.0, 5.0, fullCover, true},
		{"boundary inliers exact", domain.MinInliers, 0, 0, fullCover, true},
		{"boundary mean exact", 100, domain.MaxColorMean, 5.0, fullCover, true},
		{"boundary max exact", 100, 1.0, domain.MaxCellDist, fullCover, true},
		{"boundary coverage exact", 100, 1.0, 5.0, domain.MinAreaCoverage, true},
		{"too few inliers", domain.MinInliers - 1, 1.0, 5.0, fullCover, false},
		{"global filter (mean too high)", 100, domain.MaxColorMean + 0.1, 5.0, fullCover, false},
		{"localized edit (max too high)", 100, 1.0, domain.MaxCellDist + 0.1, fullCover, false},
		{"heavy crop (coverage too low)", 100, 1.0, 5.0, domain.MinAreaCoverage - 0.01, false},
		{"both color metrics fail", 100, 50.0, 200.0, fullCover, false},
		{"zero everything", 0, 0, 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.Decide(tt.inliers, tt.colorMean, tt.colorMax, tt.coverage)
			if got != tt.want {
				t.Errorf("Decide(%d, %v, %v, %v) = %v, want %v", tt.inliers, tt.colorMean, tt.colorMax, tt.coverage, got, tt.want)
			}
		})
	}
}

func TestFeatureSignatureCounts(t *testing.T) {
	var nilSig *domain.FeatureSignature
	if got := nilSig.KeypointCount(); got != 0 {
		t.Fatalf("nil KeypointCount = %d, want 0", got)
	}
	if got := nilSig.DescriptorCount(); got != 0 {
		t.Fatalf("nil DescriptorCount = %d, want 0", got)
	}

	sig := &domain.FeatureSignature{
		Keypoints:   make([]byte, domain.KeypointEncodedSize*2+7),
		Descriptors: make([]byte, domain.DescriptorSize*3+5),
	}
	if got := sig.KeypointCount(); got != 2 {
		t.Fatalf("KeypointCount = %d, want 2", got)
	}
	if got := sig.DescriptorCount(); got != 3 {
		t.Fatalf("DescriptorCount = %d, want 3", got)
	}
}
