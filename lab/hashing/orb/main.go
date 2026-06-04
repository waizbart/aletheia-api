// ORB keypoints + LAB color signature lab for Aletheia.
//
// Goal: match iff underlying content is unchanged.
//   MATCH expected:    quality (qX), format conversion, rotation, crop up to ~50%
//   MISMATCH expected: content-altered images, color-altering filters
//
// Pipeline per (reference, candidate) pair:
//   1. ORB descriptors (nfeatures=2000) on both images (grayscale).
//   2. BFMatcher Hamming + KNN k=2 + Lowe's ratio test.
//   3. RANSAC homography on the good matches. Count geometric inliers.
//   4. If homography found: reproject an 8x8 LAB grid from reference onto
//      candidate, measure mean LAB distance on overlapping cells. This
//      detects color-only edits that preserve keypoints (filters changing
//      hue/saturation).
//   5. Decision: match iff inliers >= MinInliers AND color_dist <= MaxColorDist.
package main

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/testdata"
)

const (
	OriginalFile = "aletheia.jpg"
	MinInliers     = 20
	MaxColorDist   = 12.0 // mean LAB residual cap (catches uniform color filters)
	MaxCellDist    = 38.0 // single-cell LAB residual cap (catches localized content changes)
	MinCoverage    = 0.9  // per-cell warp coverage required to include the cell
	LoweRatio      = 0.75
	OrbFeatures    = 2000
	GridSize       = 128
	ResizeMax      = 1024
)

var expected = map[string]string{
	"aletheia.jpg":             "match",
	"aletheia.png":             "match",
	"aletheia.gif":             "match",
	"aletheia-q10.jpg":         "match",
	"aletheia-q20.jpg":         "match",
	"aletheia-q30.jpg":         "match",
	"aletheia-q40.jpg":         "match",
	"aletheia-q50.jpg":         "match",
	"aletheia-q60.jpg":         "match",
	"aletheia-q70.jpg":         "match",
	"aletheia-q80.jpg":         "match",
	"aletheia-rotated-90.jpg":  "match",
	"aletheia-rotated-180.jpg": "match",
	"aletheia-rotated-270.jpg": "match",
	"aletheia-cropped-10p.jpg": "match",
	"aletheia-changed-1.jpg":   "mismatch",
	"aletheia-changed-2.jpg":   "mismatch",
	"aletheia-changed-3.jpg":   "mismatch",
	"aletheia-changed-4.jpg":   "mismatch",
	"aletheia-changed-5.jpg":   "mismatch",
	"aletheia-changed-6.jpg":   "mismatch",
	"aletheia-changed-7.jpg":   "mismatch",
	"aletheia-filter-1.jpg":    "mismatch",
	"aletheia-filter-2.jpg":    "mismatch",
	"aletheia-filter-3.jpg":    "mismatch",
}

func loadBGR(path string) (gocv.Mat, error) {
	mat := gocv.IMRead(path, gocv.IMReadColor)
	if mat.Empty() {
		mat.Close()
		f, err := os.Open(path)
		if err != nil {
			return gocv.NewMat(), err
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			return gocv.NewMat(), err
		}
		m, err := gocv.ImageToMatRGBA(img)
		if err != nil {
			return gocv.NewMat(), err
		}
		// gocv.ImageToMatRGBA actually writes bytes in BGRA order (the Mat's
		// channel layout follows OpenCV's native BGR convention despite the
		// function name). Using ColorRGBAToBGR here would re-swap R/B and
		// leave us with RGB-in-a-BGR-Mat, breaking downstream LAB conversion
		// (empirically dB≈+15 on saturated tones). Treat the input as BGRA.
		bgr := gocv.NewMat()
		gocv.CvtColor(m, &bgr, gocv.ColorBGRAToBGR)
		m.Close()
		mat = bgr
	}
	h, w := mat.Rows(), mat.Cols()
	longest := h
	if w > h {
		longest = w
	}
	if longest > ResizeMax {
		scale := float64(ResizeMax) / float64(longest)
		resized := gocv.NewMat()
		gocv.Resize(mat, &resized, image.Point{X: int(float64(w) * scale), Y: int(float64(h) * scale)}, 0, 0, gocv.InterpolationArea)
		mat.Close()
		mat = resized
	}
	return mat, nil
}

type signature struct {
	kp   []gocv.KeyPoint
	desc gocv.Mat
	lab  gocv.Mat
}

func (s *signature) close() {
	s.desc.Close()
	s.lab.Close()
}

func computeSignature(bgr gocv.Mat, orb gocv.ORB) signature {
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(bgr, &gray, gocv.ColorBGRToGray)

	mask := gocv.NewMat()
	defer mask.Close()
	kp, desc := orb.DetectAndCompute(gray, mask)

	lab := gocv.NewMat()
	gocv.CvtColor(bgr, &lab, gocv.ColorBGRToLab)

	return signature{kp: kp, desc: desc, lab: lab}
}

func meanLAB(region gocv.Mat) [3]float64 {
	s := region.Mean()
	return [3]float64{s.Val1, s.Val2, s.Val3}
}

// colorResidual warps the candidate image into the reference coordinate system
// using H^-1 (so pixels align to the reference grid), then compares LAB means
// per GridSize×GridSize cell. Returns (mean distance across valid cells,
// max distance across valid cells, count of valid cells).
//
// Warping into reference space lets us detect LOCAL content changes (a single
// altered patch shows up as one high-residual cell), which a global aggregate
// would dilute to near zero when most of the image is unchanged.
func colorResidual(refLab, candLab, H gocv.Mat) (mean, max float64, cells int) {
	if H.Empty() {
		return math.Inf(1), math.Inf(1), 0
	}
	Hinv := gocv.NewMat()
	defer Hinv.Close()
	if det := gocv.Invert(H, &Hinv, 0); det == 0 {
		return math.Inf(1), math.Inf(1), 0
	}

	rh, rw := refLab.Rows(), refLab.Cols()
	warped := gocv.NewMat()
	defer warped.Close()
	gocv.WarpPerspectiveWithParams(candLab, &warped, Hinv, image.Point{X: rw, Y: rh},
		gocv.InterpolationLinear, gocv.BorderConstant, color.RGBA{0, 128, 128, 0})

	// Build a binary coverage mask: warp an all-255 image with the same H^-1.
	// Cells with < 25% coverage (outside the homography's valid region) are skipped.
	ones := gocv.NewMatWithSize(candLab.Rows(), candLab.Cols(), gocv.MatTypeCV8UC1)
	defer ones.Close()
	ones.SetTo(gocv.NewScalar(255, 0, 0, 0))
	mask := gocv.NewMat()
	defer mask.Close()
	gocv.WarpPerspectiveWithParams(ones, &mask, Hinv, image.Point{X: rw, Y: rh},
		gocv.InterpolationNearestNeighbor, gocv.BorderConstant, color.RGBA{0, 0, 0, 0})

	total := 0.0
	maxD := 0.0
	count := 0
	for gy := 0; gy < GridSize; gy++ {
		for gx := 0; gx < GridSize; gx++ {
			x0 := rw * gx / GridSize
			y0 := rh * gy / GridSize
			x1 := rw * (gx + 1) / GridSize
			y1 := rh * (gy + 1) / GridSize
			rect := image.Rect(x0, y0, x1, y1)

			mRegion := mask.Region(rect)
			coverage := mRegion.Mean().Val1 / 255.0
			mRegion.Close()
			if coverage < MinCoverage {
				continue
			}

			rRegion := refLab.Region(rect)
			cRegion := warped.Region(rect)
			rm := meanLAB(rRegion)
			cm := meanLAB(cRegion)
			rRegion.Close()
			cRegion.Close()

			dl := rm[0] - cm[0]
			da := rm[1] - cm[1]
			db := rm[2] - cm[2]
			d := math.Sqrt(dl*dl + da*da + db*db)
			total += d
			if d > maxD {
				maxD = d
			}
			count++
		}
	}
	if count == 0 {
		return math.Inf(1), math.Inf(1), 0
	}
	return total / float64(count), maxD, count
}


func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type matchResult struct {
	inliers   int
	colorMean float64
	colorMax  float64
	cells     int
}

func matchPair(ref, cand signature) matchResult {
	if ref.desc.Empty() || cand.desc.Empty() || len(ref.kp) < 4 || len(cand.kp) < 4 {
		return matchResult{colorMean: math.Inf(1), colorMax: math.Inf(1)}
	}
	matcher := gocv.NewBFMatcherWithParams(gocv.NormHamming, false)
	defer matcher.Close()
	knn := matcher.KnnMatch(ref.desc, cand.desc, 2)

	good := make([]gocv.DMatch, 0, len(knn))
	for _, pair := range knn {
		if len(pair) < 2 {
			continue
		}
		if pair[0].Distance < LoweRatio*pair[1].Distance {
			good = append(good, pair[0])
		}
	}
	if len(good) < 8 {
		return matchResult{colorMean: math.Inf(1), colorMax: math.Inf(1)}
	}

	src := gocv.NewMatWithSize(len(good), 1, gocv.MatTypeCV32FC2)
	dst := gocv.NewMatWithSize(len(good), 1, gocv.MatTypeCV32FC2)
	defer src.Close()
	defer dst.Close()
	for i, m := range good {
		p1 := ref.kp[m.QueryIdx]
		p2 := cand.kp[m.TrainIdx]
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
		return matchResult{colorMean: math.Inf(1), colorMax: math.Inf(1)}
	}
	inliers := 0
	for i := 0; i < mask.Rows(); i++ {
		if mask.GetUCharAt(i, 0) != 0 {
			inliers++
		}
	}

	cmean, cmax, cells := colorResidual(ref.lab, cand.lab, H)
	return matchResult{inliers: inliers, colorMean: cmean, colorMax: cmax, cells: cells}
}

func decide(r matchResult) string {
	if r.inliers >= MinInliers && r.colorMean <= MaxColorDist && r.colorMax <= MaxCellDist {
		return "match"
	}
	return "mismatch"
}

func main() {
	testdataDir := testdata.Curated("aletheia")
	refPath := filepath.Join(testdataDir, OriginalFile)

	orb := gocv.NewORBWithParams(OrbFeatures, 1.2, 8, 31, 0, 2, gocv.ORBScoreTypeHarris, 31, 20)
	defer orb.Close()

	refBGR, err := loadBGR(refPath)
	if err != nil {
		log.Fatalf("load ref: %v", err)
	}
	defer refBGR.Close()
	refSig := computeSignature(refBGR, orb)
	defer refSig.close()

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		log.Fatalf("read testdata: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	header := fmt.Sprintf("%-32s %-10s %-10s %-5s %8s %8s %8s %6s", "file", "expected", "actual", "flag", "inliers", "mean", "max", "cells")
	fmt.Println(header)
	fmt.Println(dashes(len(header)))

	passes, total := 0, 0
	for _, name := range names {
		p := filepath.Join(testdataDir, name)
		candBGR, err := loadBGR(p)
		if err != nil {
			fmt.Printf("%-32s ERROR: %v\n", name, err)
			continue
		}
		candSig := computeSignature(candBGR, orb)
		r := matchPair(refSig, candSig)
		candSig.close()
		candBGR.Close()

		actual := decide(r)
		exp, ok := expected[name]
		flag := "--"
		if ok {
			total++
			if exp == actual {
				passes++
				flag = "OK"
			} else {
				flag = "FAIL"
			}
		} else {
			exp = "?"
		}
		fmt_ := func(v float64) string {
			if math.IsInf(v, 0) {
				return "     inf"
			}
			return fmt.Sprintf("%8.2f", v)
		}
		fmt.Printf("%-32s %-10s %-10s %-5s %8d %s %s %6d\n", name, exp, actual, flag, r.inliers, fmt_(r.colorMean), fmt_(r.colorMax), r.cells)
	}
	fmt.Println(dashes(len(header)))
	fmt.Printf("Passed: %d/%d   (MinInliers=%d, MaxColorDist=%.1f, MaxCellDist=%.1f)\n", passes, total, MinInliers, MaxColorDist, MaxCellDist)
}

func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}
