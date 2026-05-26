package domain

const (
	MinInliers = 20
	// MaxColorMean is the per-image average LAB distance allowed between the
	// stored reference and the warped candidate. The worst legitimate positive
	// in the testdata matrix (aletheia-q10.jpg, deep JPEG quality 10) measures
	// ~5.8; real-world WhatsApp recompressions land around ~1. Filter-style
	// global tone/saturation edits land around ~11+. Keeping the threshold at
	// 8.0 leaves >2 of headroom above the worst positive while decisively
	// rejecting filters.
	MaxColorMean = 8.0
	// MaxCellDist is the upper bound on a single cell's LAB distance. Catches
	// localized alterations (logo swap, emoji sticker, small paint-over) that
	// concentrate the difference in a tiny region — the per-image mean barely
	// moves, but a few cells spike. Tightly tuned against the curated matrix
	// (aletheia-changed-* sit at 39-181, aletheia-rotated-* peak at 37).
	MaxCellDist = 38.0
	MinCoverage = 0.9
	GridSize    = 128
	OrbFeatures = 2000
	LoweRatio   = 0.75
	ResizeMax   = 1024
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

func Decide(inliers int, colorMean, colorMax float64) bool {
	return inliers >= MinInliers && colorMean <= MaxColorMean && colorMax <= MaxCellDist
}
