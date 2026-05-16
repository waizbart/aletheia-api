package domain

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"math/bits"
	"sort"
)

const (
	pHashDCTSize     = 32
	pHashBlock       = 16
	MaxPHashDistance = 96
)

func PHash256(content []byte) *[32]byte {
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil
	}
	h := pHashFromImage(img)
	return &h
}

// PHash256Variants returns four rotation variants (0°, 90°, 180°, 270°) of the
// candidate's pHash. Used at verify time so the pre-filter can match a stored
// pHash regardless of the candidate's orientation.
func PHash256Variants(content []byte) [][32]byte {
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil
	}
	out := make([][32]byte, 4)
	out[0] = pHashFromImage(img)
	out[1] = pHashFromImage(rotate90(img))
	out[2] = pHashFromImage(rotate180(img))
	out[3] = pHashFromImage(rotate270(img))
	return out
}

func pHashFromImage(img image.Image) [32]byte {
	pixels := resizeAndGrayscale(img, pHashDCTSize, pHashDCTSize)
	var dct [pHashDCTSize][pHashDCTSize]float64
	dct2D(&pixels, &dct)

	var values [pHashBlock * pHashBlock]float64
	idx := 0
	for y := 0; y < pHashBlock; y++ {
		for x := 0; x < pHashBlock; x++ {
			values[idx] = dct[y][x]
			idx++
		}
	}
	median := medianFloat64(values[:])

	var hash [32]byte
	for i := 0; i < pHashBlock*pHashBlock; i++ {
		if values[i] > median {
			hash[i/8] |= 1 << (7 - (i % 8))
		}
	}
	return hash
}

func rotate90(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate270(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func Hamming256(a, b [32]byte) int {
	d := 0
	for i := 0; i < 32; i++ {
		d += bits.OnesCount8(a[i] ^ b[i])
	}
	return d
}

func resizeAndGrayscale(img image.Image, w, h int) [pHashDCTSize][pHashDCTSize]float64 {
	var dst [pHashDCTSize][pHashDCTSize]float64
	bounds := img.Bounds()
	for y := 0; y < h; y++ {
		sy := bounds.Min.Y + (y*bounds.Dy())/h
		for x := 0; x < w; x++ {
			sx := bounds.Min.X + (x*bounds.Dx())/w
			r, g, b, _ := img.At(sx, sy).RGBA()
			gray := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
			dst[y][x] = gray
		}
	}
	return dst
}

func dct2D(input, output *[pHashDCTSize][pHashDCTSize]float64) {
	const N = pHashDCTSize
	for u := 0; u < N; u++ {
		for v := 0; v < N; v++ {
			var sum float64
			for x := 0; x < N; x++ {
				for y := 0; y < N; y++ {
					cos1 := math.Cos((float64(2*x+1) * float64(u) * math.Pi) / float64(2*N))
					cos2 := math.Cos((float64(2*y+1) * float64(v) * math.Pi) / float64(2*N))
					sum += input[x][y] * cos1 * cos2
				}
			}
			cu := 1.0
			if u == 0 {
				cu = 1.0 / math.Sqrt2
			}
			cv := 1.0
			if v == 0 {
				cv = 1.0 / math.Sqrt2
			}
			output[u][v] = 0.25 * cu * cv * sum
		}
	}
}

func medianFloat64(nums []float64) float64 {
	n := len(nums)
	cp := make([]float64, n)
	copy(cp, nums)
	sort.Float64s(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}
