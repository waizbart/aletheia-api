package main

import (
	"image"
	"image/color"

	"github.com/disintegration/imaging"
	"github.com/lucasb-eyer/go-colorful"
)

func computeColorHash(img image.Image) []float32 {
	nrgba := imageToRGBA(img)
	b := nrgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if maxDim := maxInt(w, h); maxDim > ColorPreprocessMax {
		nrgba = imaging.Fit(nrgba, ColorPreprocessMax, ColorPreprocessMax, imaging.Linear)
		b = nrgba.Bounds()
		w, h = b.Dx(), b.Dy()
	}

	targetW := maxInt(4, (w/4)*4)
	targetH := maxInt(4, (h/4)*4)
	resized := imaging.Resize(nrgba, targetW, targetH, imaging.Linear)
	rb := resized.Bounds()
	rw := rb.Dx()
	rh := rb.Dy()
	cellW := rw / ColorGridSize
	cellH := rh / ColorGridSize

	allFeatures := make([]float32, 0, colorTotalBins)

	for gy := 0; gy < ColorGridSize; gy++ {
		for gx := 0; gx < ColorGridSize; gx++ {
			x0 := rb.Min.X + gx*cellW
			y0 := rb.Min.Y + gy*cellH
			x1 := x0 + cellW
			y1 := y0 + cellH

			hHist := make([]float64, ColorHistBins)
			sHist := make([]float64, ColorHistBins)
			vHist := make([]float64, ColorHistBins)
			lHist := make([]float64, ColorHistBins)
			aHist := make([]float64, ColorHistBins)
			bHist := make([]float64, ColorHistBins)

			var totalPixels float64

			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					i := resized.PixOffset(x, y)
					rgba := color.RGBA{
						R: resized.Pix[i],
						G: resized.Pix[i+1],
						B: resized.Pix[i+2],
						A: 255,
					}
					c, _ := colorful.MakeColor(rgba)
					hVal, sVal, vVal := c.Hsv()
					lVal, aVal, bVal := c.Lab()

					hBin := int(hVal / 360.0 * float64(ColorHistBins))
					if hBin >= ColorHistBins {
						hBin = ColorHistBins - 1
					}
					hHist[hBin]++

					sBin := int(sVal * float64(ColorHistBins))
					if sBin >= ColorHistBins {
						sBin = ColorHistBins - 1
					}
					sHist[sBin]++

					vBin := int(vVal * float64(ColorHistBins))
					if vBin >= ColorHistBins {
						vBin = ColorHistBins - 1
					}
					vHist[vBin]++

					lBin := int(lVal / 100.0 * float64(ColorHistBins))
					if lBin >= ColorHistBins {
						lBin = ColorHistBins - 1
					}
					if lBin < 0 {
						lBin = 0
					}
					lHist[lBin]++

					aNorm := (aVal + 128.0) / 255.0
					aBin := int(aNorm * float64(ColorHistBins))
					if aBin >= ColorHistBins {
						aBin = ColorHistBins - 1
					}
					if aBin < 0 {
						aBin = 0
					}
					aHist[aBin]++

					bNorm := (bVal + 128.0) / 255.0
					bBin := int(bNorm * float64(ColorHistBins))
					if bBin >= ColorHistBins {
						bBin = ColorHistBins - 1
					}
					if bBin < 0 {
						bBin = 0
					}
					bHist[bBin]++

					totalPixels++
				}
			}

			if totalPixels > 0 {
				normalizeHist(hHist, totalPixels)
				normalizeHist(sHist, totalPixels)
				normalizeHist(vHist, totalPixels)
				normalizeHist(lHist, totalPixels)
				normalizeHist(aHist, totalPixels)
				normalizeHist(bHist, totalPixels)
			}

			for _, val := range hHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range sHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range vHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range lHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range aHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range bHist {
				allFeatures = append(allFeatures, float32(val))
			}
		}
	}
	return allFeatures
}

func normalizeHist(hist []float64, total float64) {
	if total == 0 {
		return
	}
	for i := range hist {
		hist[i] /= total
	}
}

func computeColorThresholds(features []float32) [6]float32 {
	var thresholds [6]float32
	for ch := 0; ch < 6; ch++ {
		var sum float64
		for region := 0; region < ColorGridSize*ColorGridSize; region++ {
			baseIdx := region*192 + ch*ColorHistBins
			for bin := 0; bin < ColorHistBins; bin++ {
				sum += float64(features[baseIdx+bin])
			}
		}
		thresholds[ch] = float32(sum / 512.0)
	}
	return thresholds
}

func binarizeColorVector(features []float32, thresholds [6]float32) []byte {
	numBits := len(features)
	numBytes := (numBits + 7) / 8
	result := make([]byte, numBytes)
	for region := 0; region < ColorGridSize*ColorGridSize; region++ {
		for ch := 0; ch < 6; ch++ {
			baseIdx := region*192 + ch*ColorHistBins
			for bin := 0; bin < ColorHistBins; bin++ {
				i := baseIdx + bin
				if features[i] >= thresholds[ch] {
					byteIdx := i / 8
					bitIdx := uint(i % 8)
					result[byteIdx] |= 1 << bitIdx
				}
			}
		}
	}
	return result
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
