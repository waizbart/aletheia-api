package domain_test

import (
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name      string
		inliers   int
		colorMean float64
		colorMax  float64
		want      bool
	}{
		{"all pass", 100, 1.0, 5.0, true},
		{"boundary inliers exact", domain.MinInliers, 0, 0, true},
		{"boundary mean exact", 100, domain.MaxColorMean, 5.0, true},
		{"boundary max exact", 100, 1.0, domain.MaxCellDist, true},
		{"too few inliers", domain.MinInliers - 1, 1.0, 5.0, false},
		{"global filter (mean too high)", 100, domain.MaxColorMean + 0.1, 5.0, false},
		{"localized edit (max too high)", 100, 1.0, domain.MaxCellDist + 0.1, false},
		{"both color metrics fail", 100, 50.0, 200.0, false},
		{"zero everything", 0, 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.Decide(tt.inliers, tt.colorMean, tt.colorMax)
			if got != tt.want {
				t.Errorf("Decide(%d, %v, %v) = %v, want %v", tt.inliers, tt.colorMean, tt.colorMax, got, tt.want)
			}
		})
	}
}
