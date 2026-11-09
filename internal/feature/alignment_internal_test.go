//go:build integration

package feature

import (
	"os"
	"path/filepath"
	"testing"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/testdata"
)

// cardinalRotH is the analytic reference→candidate homography for a clockwise
// cardinal rotation of a refW×refH reference, matching gocv.Rotate's pixel
// permutation.
func cardinalRotH(deg, refW, refH int) gocv.Mat {
	H := gocv.NewMatWithSize(3, 3, gocv.MatTypeCV64F)
	H.SetTo(gocv.NewScalar(0, 0, 0, 0))
	H.SetDoubleAt(2, 2, 1)
	switch deg {
	case 90: // (x,y) → (refH-1-y, x)
		H.SetDoubleAt(0, 1, -1)
		H.SetDoubleAt(0, 2, float64(refH-1))
		H.SetDoubleAt(1, 0, 1)
	case 180: // (x,y) → (refW-1-x, refH-1-y)
		H.SetDoubleAt(0, 0, -1)
		H.SetDoubleAt(0, 2, float64(refW-1))
		H.SetDoubleAt(1, 1, -1)
		H.SetDoubleAt(1, 2, float64(refH-1))
	case 270: // (x,y) → (y, refW-1-x)
		H.SetDoubleAt(0, 1, 1)
		H.SetDoubleAt(1, 0, -1)
		H.SetDoubleAt(1, 2, float64(refW-1))
	}
	return H
}

// ransacH re-derives the reference→candidate homography exactly as matchPair
// does, and reports the inlier count alongside it.
func ransacH(refKp, candKp []gocv.KeyPoint, refDesc, candDesc gocv.Mat) (gocv.Mat, int) {
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
		return gocv.NewMat(), 0
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
	inliers := 0
	for i := 0; i < mask.Rows(); i++ {
		if mask.GetUCharAt(i, 0) != 0 {
			inliers++
		}
	}
	return H, inliers
}

// TestCardinalRotation_ResidualIsAlignmentBound characterises why cardinal
// rotations sit in the borderline stratum of the transform taxonomy.
//
// A 90/180/270° rotation is a lossless permutation of pixels, so the colour
// residual against the reference grid should be near zero. It is — but only
// when the warp is exact. Under the RANSAC-estimated homography the residual is
// far larger, because the 128×128 grid over an 800×600 reference gives cells of
// roughly 6×5 px: a sub-pixel alignment error moves a high-frequency cell's
// mean by tens of LAB units, which the per-cell gate reads as a localized edit.
//
// The test pins the diagnosis rather than a tuning: the colour gate and the
// pHash pre-filter are sound, and geometric precision is the binding limit. Any
// change that makes rotations pass must come from a more accurate warp, not
// from relaxing MaxCellDist.
func TestCardinalRotation_ResidualIsAlignmentBound(t *testing.T) {
	e := NewOpenCVExtractor()
	defer e.Close()

	baseB, err := os.ReadFile(filepath.Join(testdata.Curated("aletheia"), "aletheia.jpg"))
	if err != nil {
		t.Fatalf("read reference image: %v", err)
	}
	refSig, err := e.Compute(t.Context(), baseB)
	if err != nil {
		t.Fatalf("compute reference signature: %v", err)
	}
	refKp, err := decodeKeypoints(refSig.Keypoints)
	if err != nil {
		t.Fatalf("decode reference keypoints: %v", err)
	}
	refDesc, err := matFromDescriptors(refSig.Descriptors)
	if err != nil {
		t.Fatalf("reference descriptors: %v", err)
	}
	defer refDesc.Close()

	refMat, err := decodeBGR(baseB)
	if err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	defer refMat.Close()

	for _, tc := range []struct {
		name string
		deg  int
		mode gocv.RotateFlag
	}{
		{"90", 90, gocv.Rotate90Clockwise},
		{"180", 180, gocv.Rotate180Clockwise},
		{"270", 270, gocv.Rotate90CounterClockwise},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rotated := gocv.NewMat()
			defer rotated.Close()
			gocv.Rotate(refMat, &rotated, tc.mode)
			candB, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, rotated,
				[]int{gocv.IMWriteJpegQuality, 90})
			if err != nil {
				t.Fatalf("encode rotated: %v", err)
			}
			defer candB.Close()
			varB := candB.GetBytes()

			// The pre-filter must find the rotation: one of the four pHash
			// variants is the inverse rotation, so the distance is near zero.
			refPH := domain.PHash256(baseB)
			if refPH == nil {
				t.Fatal("reference pHash is nil")
			}
			minHamm := domain.PHashBandCount * 8
			for _, v := range domain.PHash256Variants(varB) {
				if d := domain.Hamming256(v, *refPH); d < minHamm {
					minHamm = d
				}
			}
			if minHamm > domain.MaxPHashDistance {
				t.Errorf("pre-filter would drop the rotation: pHash distance %d > %d",
					minHamm, domain.MaxPHashDistance)
			}

			candSig, err := e.Compute(t.Context(), varB)
			if err != nil {
				t.Fatalf("compute candidate signature: %v", err)
			}
			candKp, err := decodeKeypoints(candSig.Keypoints)
			if err != nil {
				t.Fatalf("decode candidate keypoints: %v", err)
			}
			candDesc, err := matFromDescriptors(candSig.Descriptors)
			if err != nil {
				t.Fatalf("candidate descriptors: %v", err)
			}
			defer candDesc.Close()
			candLab, err := decodeLAB(varB)
			if err != nil {
				t.Fatalf("candidate LAB: %v", err)
			}
			defer candLab.Close()

			// Exact warp: the residual collapses and every gate passes.
			exact := cardinalRotH(tc.deg, refSig.RefWidth, refSig.RefHeight)
			defer exact.Close()
			eMean, eMax, _, eCov := colorResidual(refSig.ColorGrid,
				refSig.RefWidth, refSig.RefHeight, candLab, exact)
			if eMax > domain.MaxCellDist {
				t.Errorf("exact warp: ColorMax %.1f exceeds %.1f — a lossless rotation must not trip the per-cell gate",
					eMax, domain.MaxCellDist)
			}
			if eMean > domain.MaxColorMean {
				t.Errorf("exact warp: ColorMean %.2f exceeds %.1f", eMean, domain.MaxColorMean)
			}
			if eCov < domain.MinAreaCoverage {
				t.Errorf("exact warp: coverage %.3f below %.2f", eCov, domain.MinAreaCoverage)
			}

			// Estimated warp: ORB and RANSAC find the rotation comfortably, so
			// whenever the decision goes the wrong way it is the per-cell colour
			// gate that trips — never the inlier or coverage gate.
			est, inliers := ransacH(refKp, candKp, refDesc, candDesc)
			defer est.Close()
			if est.Empty() {
				t.Fatal("estimated homography is empty")
			}
			rMean, rMax, _, rCov := colorResidual(refSig.ColorGrid,
				refSig.RefWidth, refSig.RefHeight, candLab, est)
			if inliers < domain.MinInliers {
				t.Errorf("estimated warp: inliers %d below %d — the geometric stage is not the limit",
					inliers, domain.MinInliers)
			}
			if rCov < domain.MinAreaCoverage {
				t.Errorf("estimated warp: coverage %.3f below %.2f", rCov, domain.MinAreaCoverage)
			}

			t.Logf("rotate %s: exact warp mean=%.2f max=%.1f | estimated warp mean=%.2f max=%.1f inliers=%d",
				tc.name, eMean, eMax, rMean, rMax, inliers)
		})
	}
}
