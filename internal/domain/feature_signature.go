package domain

const (
	MinInliers   = 20
	MaxColorMean = 12.0
	MaxCellDist  = 38.0
	MinCoverage  = 0.9
	GridSize     = 128
	OrbFeatures  = 2000
	LoweRatio    = 0.75
	ResizeMax    = 1024
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
