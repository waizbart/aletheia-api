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
	// MinAreaCoverage is the minimum fraction of the reference image area the
	// matched candidate must cover (via the RANSAC homography, measured as the
	// share of grid cells the warped candidate fills) for a positive match.
	// Without it, an identity-breaking heavy crop passes every inlier and colour
	// check: the surviving region is pixel-identical to the reference, so the
	// per-cell residual over the *covered* cells is ~0. Coverage is essentially
	// the area ratio of the crop and is independent of image content, so the gate
	// is a clean geometric separator. Measured values (see TestOpenCVExtractor_
	// CoverageGate): crop_border 5/10/15% → 0.79/0.64/0.48; crop_border_20% and
	// heavy_crop_40% → 0.35 (these two rungs are the SAME pixel-identical image —
	// a 36%-area crop — despite opposite taxonomy labels). 0.42 keeps mild border
	// crops and rejects any crop discarding >~60% of the frame. Full-frame edits
	// (jpeg/noise/scale/rotation) sit near 1.0 and are unaffected.
	MinAreaCoverage     = 0.42
	MinCellMaskCoverage = 0.9
	GridSize            = 128
	OrbFeatures         = 2000
	LoweRatio           = 0.75
	ResizeMax           = 1024
)

const (
	// DescriptorSize is the byte length of a single ORB descriptor (256 bits).
	DescriptorSize = 32
	// KeypointEncodedSize is the byte length of a single serialized keypoint
	// (X, Y, Size, Angle, Response as float64 + Octave, ClassID as int32).
	KeypointEncodedSize = 48
	// ColorGridChannels is the number of stored channels per grid cell (L, a, b).
	ColorGridChannels = 3
	// ColorGridBytes is the exact byte length of an encoded color grid:
	// GridSize² cells × 3 channels, one byte per channel (the LAB cell mean
	// rounded to the nearest integer; OpenCV 8-bit LAB channels span 0–255).
	// Rounding drifts each channel by ≤0.5, bounding the per-cell LAB distance
	// error at ~0.87 — three orders of magnitude under MaxColorMean/MaxCellDist,
	// and measured to flip zero decisions across the transform taxonomy.
	ColorGridBytes = GridSize * GridSize * ColorGridChannels
)

type FeatureSignature struct {
	Descriptors []byte
	Keypoints   []byte
	// ColorGrid holds the per-cell mean LAB color of the normalized reference
	// image, row-major over the GridSize×GridSize grid, 3 bytes per cell.
	// It replaces the reference image previously fetched from blob storage:
	// the color-residual gate only ever reads these cell means, never pixels.
	ColorGrid []byte
	// RefWidth and RefHeight are the reference dimensions in the resized space
	// (long side capped at ResizeMax) the keypoints and grid cells live in. The
	// matcher needs them to warp the candidate into the reference frame.
	RefWidth  int
	RefHeight int
}

// HasColorGrid reports whether the signature carries a well-formed color grid
// with usable reference dimensions.
func (s *FeatureSignature) HasColorGrid() bool {
	return s != nil && len(s.ColorGrid) == ColorGridBytes && s.RefWidth > 0 && s.RefHeight > 0
}

// KeypointCount returns the number of ORB keypoints encoded in the signature.
func (s *FeatureSignature) KeypointCount() int {
	if s == nil || KeypointEncodedSize == 0 {
		return 0
	}
	return len(s.Keypoints) / KeypointEncodedSize
}

// DescriptorCount returns the number of ORB descriptors in the signature.
func (s *FeatureSignature) DescriptorCount() int {
	if s == nil || DescriptorSize == 0 {
		return 0
	}
	return len(s.Descriptors) / DescriptorSize
}

type MatchDecision struct {
	Matched   bool
	Inliers   int
	ColorMean float64
	ColorMax  float64
	Cells     int
	// Coverage is the fraction of the reference grid (GridSize²) the warped
	// candidate fills. 1.0 means the candidate spans the whole reference; a crop
	// that discards 40% of the frame lands near 0.6.
	Coverage float64
}

func Decide(inliers int, colorMean, colorMax, coverage float64) bool {
	return inliers >= MinInliers &&
		colorMean <= MaxColorMean &&
		colorMax <= MaxCellDist &&
		coverage >= MinAreaCoverage
}
