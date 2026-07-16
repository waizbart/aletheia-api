package feature

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sort"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
)

const (
	keypointBytes = 48

	orbEdgeThreshold = 31
	orbPatchSize     = 31
	// minFeatureDimension is the smallest width/height (px) the ORB pipeline can
	// process safely. Below 2*edgeThreshold the finest pyramid level has no valid
	// detection region (and the coarsest levels collapse toward 0), at which
	// point native OpenCV can read out of bounds and crash the process with a
	// SIGSEGV. Images smaller than this are rejected before DetectAndCompute.
	minFeatureDimension = 2*orbEdgeThreshold + 1

	// thumbnailLongSide is the long-side pixel size of dashboard thumbnails
	// rendered from a stored color grid.
	thumbnailLongSide = 420
)

type OpenCVExtractor struct{}

func NewOpenCVExtractor() *OpenCVExtractor {
	return &OpenCVExtractor{}
}

func (e *OpenCVExtractor) Close() {}

func (e *OpenCVExtractor) Compute(_ context.Context, content []byte) (*domain.FeatureSignature, error) {
	bgr, err := decodeBGR(content)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	defer bgr.Close()

	if bgr.Empty() {
		return nil, fmt.Errorf("decode image: empty mat")
	}

	resized := resizeBGR(bgr, domain.ResizeMax)
	defer resized.Close()

	if resized.Cols() < minFeatureDimension || resized.Rows() < minFeatureDimension {
		return nil, fmt.Errorf("image too small for feature extraction: %dx%d (min %d per side)", resized.Cols(), resized.Rows(), minFeatureDimension)
	}

	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(resized, &gray, gocv.ColorBGRToGray)

	orb := gocv.NewORBWithParams(domain.OrbFeatures, 1.2, 8, orbEdgeThreshold, 0, 2, gocv.ORBScoreTypeHarris, orbPatchSize, 20)
	defer orb.Close()

	mask := gocv.NewMat()
	defer mask.Close()
	keypoints, descriptors := orb.DetectAndCompute(gray, mask)
	defer descriptors.Close()

	if descriptors.Empty() || len(keypoints) == 0 {
		return nil, fmt.Errorf("no features detected")
	}

	descBytes := descriptors.ToBytes()
	descCopy := make([]byte, len(descBytes))
	copy(descCopy, descBytes)

	kpBytes := encodeKeypoints(keypoints)

	lab := gocv.NewMat()
	defer lab.Close()
	gocv.CvtColor(resized, &lab, gocv.ColorBGRToLab)

	return &domain.FeatureSignature{
		Descriptors: descCopy,
		Keypoints:   kpBytes,
		ColorGrid:   colorGridOf(lab),
		RefWidth:    resized.Cols(),
		RefHeight:   resized.Rows(),
	}, nil
}

// Match compares the stored reference signature against the candidate. The
// reference side of the color check reads the signature's color grid — the
// per-cell LAB means captured at certify time — so no reference image bytes
// are needed; only the candidate image (available at request time) is decoded.
func (e *OpenCVExtractor) Match(_ context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error) {
	if refSig == nil || candSig == nil {
		return domain.MatchDecision{}, fmt.Errorf("match: nil signature")
	}
	if !refSig.HasColorGrid() {
		return domain.MatchDecision{}, fmt.Errorf("match: reference signature has no color grid")
	}

	refKp, err := decodeKeypoints(refSig.Keypoints)
	if err != nil {
		return domain.MatchDecision{}, fmt.Errorf("match: decode ref keypoints: %w", err)
	}
	candKp, err := decodeKeypoints(candSig.Keypoints)
	if err != nil {
		return domain.MatchDecision{}, fmt.Errorf("match: decode cand keypoints: %w", err)
	}

	refDesc, err := matFromDescriptors(refSig.Descriptors)
	if err != nil {
		return domain.MatchDecision{}, fmt.Errorf("match: ref descriptors: %w", err)
	}
	defer refDesc.Close()
	candDesc, err := matFromDescriptors(candSig.Descriptors)
	if err != nil {
		return domain.MatchDecision{}, fmt.Errorf("match: cand descriptors: %w", err)
	}
	defer candDesc.Close()

	candLab, err := decodeLAB(candImage)
	if err != nil {
		return domain.MatchDecision{}, fmt.Errorf("match: cand LAB: %w", err)
	}
	defer candLab.Close()

	return matchPair(refKp, refDesc, refSig.ColorGrid, refSig.RefWidth, refSig.RefHeight, candKp, candDesc, candLab), nil
}

func matchPair(refKp []gocv.KeyPoint, refDesc gocv.Mat, refGrid []byte, refW, refH int, candKp []gocv.KeyPoint, candDesc gocv.Mat, candLab gocv.Mat) domain.MatchDecision {
	res := domain.MatchDecision{ColorMean: math.Inf(1), ColorMax: math.Inf(1)}
	if refDesc.Empty() || candDesc.Empty() || len(refKp) < 4 || len(candKp) < 4 {
		return res
	}
	matcher := gocv.NewBFMatcherWithParams(gocv.NormHamming, false)
	defer matcher.Close()
	knn := matcher.KnnMatch(refDesc, candDesc, 2)

	good := make([]gocv.DMatch, 0, len(knn))
	for _, pair := range knn {
		if len(pair) < 2 {
			continue
		}
		if pair[0].Distance < domain.LoweRatio*pair[1].Distance {
			good = append(good, pair[0])
		}
	}
	if len(good) < 8 {
		return res
	}

	src := gocv.NewMatWithSize(len(good), 1, gocv.MatTypeCV32FC2)
	dst := gocv.NewMatWithSize(len(good), 1, gocv.MatTypeCV32FC2)
	defer src.Close()
	defer dst.Close()
	for i, m := range good {
		p1 := refKp[m.QueryIdx]
		p2 := candKp[m.TrainIdx]
		src.SetFloatAt(i, 0, float32(p1.X))
		src.SetFloatAt(i, 1, float32(p1.Y))
		dst.SetFloatAt(i, 0, float32(p2.X))
		dst.SetFloatAt(i, 1, float32(p2.Y))
	}
	mask := gocv.NewMat()
	defer mask.Close()
	H := gocv.FindHomography(src, &dst, gocv.HomograpyMethodRANSAC, 5.0, &mask, 2000, 0.995)
	defer H.Close()
	if H.Empty() {
		return res
	}
	inliers := 0
	for i := 0; i < mask.Rows(); i++ {
		if mask.GetUCharAt(i, 0) != 0 {
			inliers++
		}
	}

	mean, max, cells, coverage := colorResidual(refGrid, refW, refH, candLab, H)
	res.Inliers = inliers
	res.ColorMean = mean
	res.ColorMax = max
	res.Cells = cells
	res.Coverage = coverage
	res.Matched = domain.Decide(inliers, mean, max, coverage)
	return res
}

// colorResidual warps the candidate back into the reference frame and measures,
// per grid cell, the LAB distance to the reference's stored per-cell means. It
// returns the mean and max per-cell distance, the number of covered cells, and
// the coverage fraction (covered cells / total grid cells).
//
// A uniform brightness/exposure shift moves every cell's L channel by roughly
// the same amount — an identity-preserving edit our taxonomy labels as a match.
// To stay robust to it, the global luminance offset (median ΔL over covered
// cells) is subtracted before computing each cell's distance. Chroma (a/b) is
// left untouched, so global colour filters (sepia, hue, saturation) and
// localized edits (overlays, recolours) still register their full residual.
func colorResidual(refGrid []byte, rw, rh int, candLab, H gocv.Mat) (mean, max float64, cells int, coverage float64) {
	if H.Empty() {
		return math.Inf(1), math.Inf(1), 0, 0
	}
	Hinv := gocv.NewMat()
	defer Hinv.Close()
	if det := gocv.Invert(H, &Hinv, 0); det == 0 {
		return math.Inf(1), math.Inf(1), 0, 0
	}

	warped := gocv.NewMat()
	defer warped.Close()
	gocv.WarpPerspectiveWithParams(candLab, &warped, Hinv, image.Point{X: rw, Y: rh},
		gocv.InterpolationLinear, gocv.BorderConstant, color.RGBA{0, 128, 128, 0})

	ones := gocv.NewMatWithSize(candLab.Rows(), candLab.Cols(), gocv.MatTypeCV8UC1)
	defer ones.Close()
	ones.SetTo(gocv.NewScalar(255, 0, 0, 0))
	mask := gocv.NewMat()
	defer mask.Close()
	gocv.WarpPerspectiveWithParams(ones, &mask, Hinv, image.Point{X: rw, Y: rh},
		gocv.InterpolationNearestNeighbor, gocv.BorderConstant, color.RGBA{0, 0, 0, 0})

	grid := domain.GridSize
	type cellDelta struct{ dl, da, db float64 }
	deltas := make([]cellDelta, 0, grid*grid)
	dls := make([]float64, 0, grid*grid)
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x0 := rw * gx / grid
			y0 := rh * gy / grid
			x1 := rw * (gx + 1) / grid
			y1 := rh * (gy + 1) / grid
			rect := image.Rect(x0, y0, x1, y1)

			mRegion := mask.Region(rect)
			cov := mRegion.Mean().Val1 / 255.0
			mRegion.Close()
			if cov < domain.MinCellMaskCoverage {
				continue
			}

			cRegion := warped.Region(rect)
			cs := cRegion.Mean()
			cRegion.Close()

			off := (gy*grid + gx) * domain.ColorGridChannels
			d := cellDelta{
				dl: float64(refGrid[off]) - cs.Val1,
				da: float64(refGrid[off+1]) - cs.Val2,
				db: float64(refGrid[off+2]) - cs.Val3,
			}
			deltas = append(deltas, d)
			dls = append(dls, d.dl)
		}
	}
	count := len(deltas)
	if count == 0 {
		return math.Inf(1), math.Inf(1), 0, 0
	}

	// Neutralize the global luminance offset (uniform exposure/brightness shift).
	lOff := medianFloat(dls)

	total := 0.0
	maxD := 0.0
	for _, dc := range deltas {
		dl := dc.dl - lOff
		d := math.Sqrt(dl*dl + dc.da*dc.da + dc.db*dc.db)
		total += d
		if d > maxD {
			maxD = d
		}
	}
	coverage = float64(count) / float64(grid*grid)
	return total / float64(count), maxD, count, coverage
}

// medianFloat returns the median of xs. xs is sorted in place.
func medianFloat(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	sort.Float64s(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// colorGridOf encodes the per-cell mean LAB color of lab into the storage
// layout described by domain.ColorGridBytes: row-major GridSize×GridSize cells,
// 3 bytes per cell, each channel mean rounded to the nearest integer. The cell
// rectangles use the same integer division as colorResidual so certify-time
// means and match-time cells always line up.
func colorGridOf(lab gocv.Mat) []byte {
	grid := domain.GridSize
	rh, rw := lab.Rows(), lab.Cols()
	out := make([]byte, domain.ColorGridBytes)
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x0 := rw * gx / grid
			y0 := rh * gy / grid
			x1 := rw * (gx + 1) / grid
			y1 := rh * (gy + 1) / grid
			region := lab.Region(image.Rect(x0, y0, x1, y1))
			m := region.Mean()
			region.Close()
			off := (gy*grid + gx) * domain.ColorGridChannels
			out[off] = roundToByte(m.Val1)
			out[off+1] = roundToByte(m.Val2)
			out[off+2] = roundToByte(m.Val3)
		}
	}
	return out
}

func roundToByte(v float64) byte {
	r := math.Round(v)
	if r < 0 {
		return 0
	}
	if r > 255 {
		return 255
	}
	return byte(r)
}

// RenderColorGridPNG renders a stored color grid back to a small PNG at the
// reference aspect ratio. It backs the observability dashboard's candidate
// thumbnails now that no reference image is stored.
func (e *OpenCVExtractor) RenderColorGridPNG(grid []byte, refW, refH int) ([]byte, error) {
	if len(grid) != domain.ColorGridBytes || refW <= 0 || refH <= 0 {
		return nil, fmt.Errorf("render color grid: invalid grid (%d bytes, %dx%d)", len(grid), refW, refH)
	}
	g := domain.GridSize
	lab := gocv.NewMatWithSize(g, g, gocv.MatTypeCV8UC3)
	defer lab.Close()
	for gy := 0; gy < g; gy++ {
		for gx := 0; gx < g; gx++ {
			off := (gy*g + gx) * domain.ColorGridChannels
			lab.SetUCharAt(gy, gx*3+0, grid[off])
			lab.SetUCharAt(gy, gx*3+1, grid[off+1])
			lab.SetUCharAt(gy, gx*3+2, grid[off+2])
		}
	}
	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(lab, &bgr, gocv.ColorLabToBGR)

	long := refW
	if refH > long {
		long = refH
	}
	scale := float64(thumbnailLongSide) / float64(long)
	sized := gocv.NewMat()
	defer sized.Close()
	gocv.Resize(bgr, &sized, image.Point{X: int(float64(refW) * scale), Y: int(float64(refH) * scale)}, 0, 0, gocv.InterpolationLinear)

	buf, err := gocv.IMEncode(gocv.PNGFileExt, sized)
	if err != nil {
		return nil, fmt.Errorf("render color grid: encode png: %w", err)
	}
	defer buf.Close()
	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())
	return out, nil
}

func decodeBGR(content []byte) (gocv.Mat, error) {
	mat, err := gocv.IMDecode(content, gocv.IMReadColor)
	if err == nil && !mat.Empty() {
		return mat, nil
	}
	if !mat.Empty() {
		mat.Close()
	}

	img, _, derr := image.Decode(bytes.NewReader(content))
	if derr != nil {
		return gocv.NewMat(), fmt.Errorf("imdecode + image.Decode failed: %v / %w", err, derr)
	}
	rgba, ierr := gocv.ImageToMatRGBA(img)
	if ierr != nil {
		return gocv.NewMat(), fmt.Errorf("ImageToMatRGBA: %w", ierr)
	}
	defer rgba.Close()

	bgr := gocv.NewMat()
	gocv.CvtColor(rgba, &bgr, gocv.ColorBGRAToBGR)
	return bgr, nil
}

// decodeLAB decodes an image to LAB at the same scale the feature extractor
// works in (capped to domain.ResizeMax on the long side). The homography used
// during Match is computed from keypoints in that resized coordinate space, so
// the candidate LAB MUST be in the same space — otherwise WarpPerspective
// samples the wrong pixels and the per-cell color residual blows up even when
// the geometry matches perfectly (observed with high-resolution phone photos
// re-uploaded via WhatsApp).
func decodeLAB(content []byte) (gocv.Mat, error) {
	bgr, err := decodeBGR(content)
	if err != nil {
		return gocv.NewMat(), err
	}
	defer bgr.Close()
	if bgr.Empty() {
		return gocv.NewMat(), fmt.Errorf("empty bgr")
	}
	resized := resizeBGR(bgr, domain.ResizeMax)
	defer resized.Close()
	lab := gocv.NewMat()
	gocv.CvtColor(resized, &lab, gocv.ColorBGRToLab)
	return lab, nil
}

func resizeBGR(src gocv.Mat, maxSide int) gocv.Mat {
	h, w := src.Rows(), src.Cols()
	longest := h
	if w > h {
		longest = w
	}
	if longest <= maxSide {
		out := gocv.NewMat()
		src.CopyTo(&out)
		return out
	}
	scale := float64(maxSide) / float64(longest)
	dst := gocv.NewMat()
	gocv.Resize(src, &dst, image.Point{X: int(float64(w) * scale), Y: int(float64(h) * scale)}, 0, 0, gocv.InterpolationArea)
	return dst
}

func matFromDescriptors(data []byte) (gocv.Mat, error) {
	if len(data) == 0 || len(data)%32 != 0 {
		return gocv.NewMat(), fmt.Errorf("invalid descriptor length %d", len(data))
	}
	rows := len(data) / 32
	return gocv.NewMatFromBytes(rows, 32, gocv.MatTypeCV8UC1, data)
}

func encodeKeypoints(kps []gocv.KeyPoint) []byte {
	buf := make([]byte, len(kps)*keypointBytes)
	for i, kp := range kps {
		off := i * keypointBytes
		binary.LittleEndian.PutUint64(buf[off+0:], math.Float64bits(kp.X))
		binary.LittleEndian.PutUint64(buf[off+8:], math.Float64bits(kp.Y))
		binary.LittleEndian.PutUint64(buf[off+16:], math.Float64bits(kp.Size))
		binary.LittleEndian.PutUint64(buf[off+24:], math.Float64bits(kp.Angle))
		binary.LittleEndian.PutUint64(buf[off+32:], math.Float64bits(kp.Response))
		binary.LittleEndian.PutUint32(buf[off+40:], uint32(int32(kp.Octave)))
		binary.LittleEndian.PutUint32(buf[off+44:], uint32(int32(kp.ClassID)))
	}
	return buf
}

func decodeKeypoints(buf []byte) ([]gocv.KeyPoint, error) {
	if len(buf)%keypointBytes != 0 {
		return nil, fmt.Errorf("invalid keypoints length %d", len(buf))
	}
	n := len(buf) / keypointBytes
	out := make([]gocv.KeyPoint, n)
	for i := 0; i < n; i++ {
		off := i * keypointBytes
		out[i] = gocv.KeyPoint{
			X:        math.Float64frombits(binary.LittleEndian.Uint64(buf[off+0:])),
			Y:        math.Float64frombits(binary.LittleEndian.Uint64(buf[off+8:])),
			Size:     math.Float64frombits(binary.LittleEndian.Uint64(buf[off+16:])),
			Angle:    math.Float64frombits(binary.LittleEndian.Uint64(buf[off+24:])),
			Response: math.Float64frombits(binary.LittleEndian.Uint64(buf[off+32:])),
			Octave:   int(int32(binary.LittleEndian.Uint32(buf[off+40:]))),
			ClassID:  int(int32(binary.LittleEndian.Uint32(buf[off+44:]))),
		}
	}
	return out, nil
}
