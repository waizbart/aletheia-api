package domain

const (
	MinInliers        = 12
	StrongInliers     = 200
	MaxColorMean      = 28.0
	MaxCellDist       = 65.0
	MinCoverage       = 0.75
	GridSize          = 128
	OrbFeatures       = 2000
	LoweRatio         = 0.75
	ResizeMax         = 1024
)

type FeatureSignature struct {
	Descriptors []byte
	Keypoints   []byte
}

type MatchDecision struct {
	Matched   bool
	Inliers   int
	ColorMean float64
	ColorMax  float64
	Cells     int
}

// Decide returns true when the geometric and colour evidence is strong enough
// to consider two images the same certified content.
//
// Two-tier logic:
//   - Strong geometric match (inliers >= StrongInliers): the RANSAC homography
//     alone is conclusive — colour check is skipped. This handles images that
//     passed through platforms (WhatsApp, Telegram, Twitter/X) that re-encode
//     JPEG and convert colour profiles (e.g. P3 → sRGB), causing large CIELab
//     deltas that have nothing to do with intentional content modification.
//   - Borderline match (MinInliers <= inliers < StrongInliers): colour check
//     is applied to rule out visually similar but distinct images.
func Decide(inliers int, colorMean, colorMax float64) bool {
	if inliers < MinInliers {
		return false
	}
	if inliers >= StrongInliers {
		return true
	}
	return colorMean <= MaxColorMean && colorMax <= MaxCellDist
}
