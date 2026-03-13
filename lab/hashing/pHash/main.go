package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"image"
	_ "image/jpeg"
	_ "image/png"
	_ "image/gif"
	"os"
	"math/bits"
	"math"
	"sort"
)

const ORIGINAL_FILE_NAME = "aletheia.jpg"
const MIN_H_DIST = 2

func main() {
	testdataDir := filepath.Join("..", "testdata")

	originalPath := filepath.Join(testdataDir, ORIGINAL_FILE_NAME)
	originalHash, err := hashFile(originalPath)
	if err != nil {
		log.Fatalf("failed to hash original: %v", err)
	}

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		log.Fatalf("failed to read testdata dir: %v", err)
	}

	fmt.Printf("Original: %s\n", originalPath)
	fmt.Printf("SHA-256:  %x\n\n", originalHash)
	fmt.Println(strings.Repeat("-", 70))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		path := filepath.Join(testdataDir, filename)
		h, err := hashFile(path)
		if err != nil {
			fmt.Printf("%-35s  ERROR: %v\n", filename, err)
			continue
		}

		hDist := hammingDistance(originalHash, h)
		match := "MISMATCH"
		if h == originalHash || hDist <= MIN_H_DIST {
			match = "MATCH"
		}

		if filename == ORIGINAL_FILE_NAME {
			filename += " (original)"
		}

		fmt.Printf("%-30s  %-10s  %-5d  %x\n", filename, match, hDist, h)
	}
}

// pHashSize defines the width and height for the discrete cosine transform in pHash.
// A typical value is 32x32 for DCT, using the top-left 8x8 block for the hash.
const (
	pHashWidth  = 32
	pHashHeight = 32
	pHashBlock  = 8 // 8x8 DCT top-left block used for hash
)

// pHashResult returns a 64-bit hash as a fixed-length array.
type pHashResult [8]byte

// hashFile computes the pHash (perceptual hash) of an image file.
func hashFile(path string) (pHashResult, error) {
	var empty pHashResult

	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return empty, err
	}

	// Step 1: Resize to pHashWidth x pHashHeight and grayscale
	gray := resizeAndGrayscale(img, pHashWidth, pHashHeight)

	// Step 2: Convert grayscale pixels to a 2D float64 slice
	var pixels [pHashHeight][pHashWidth]float64
	for y := 0; y < pHashHeight; y++ {
		for x := 0; x < pHashWidth; x++ {
			pixels[y][x] = float64(gray.GrayAt(x, y).Y)
		}
	}

	// Step 3: Compute the 2D Discrete Cosine Transform (DCT)
	var dct [pHashHeight][pHashWidth]float64
	dct2D(&pixels, &dct)

	// Step 4: Extract top-left 8x8 block of DCT coefficients (skip DC at (0,0))
	var total float64 = 0
	var values [pHashBlock * pHashBlock]float64
	idx := 0
	for y := 0; y < pHashBlock; y++ {
		for x := 0; x < pHashBlock; x++ {
			values[idx] = dct[y][x]
			total += dct[y][x]
			idx++
		}
	}
	// Step 5: Compute the median (excluding the DC coefficient at 0,0)
	var block []float64
	block = append(block, values[1:]...) // skip DC coefficient
	median := medianFloat64(block)

	// Step 6: Build hash: set bit if DCT > median, skip DC coefficient
	var hash uint64
	bit := 0
	for i := 1; i < len(values); i++ { // i == 0 is DC
		if values[i] > median {
			hash |= 1 << (63 - bit)
		}
		bit++
	}

	// Step 7: Convert uint64 to [8]byte
	var result pHashResult
	for i := 0; i < 8; i++ {
		result[i] = byte(hash >> (56 - 8*i))
	}
	return result, nil
}

// resizeAndGrayscale resizes img to w x h and converts to *image.Gray
func resizeAndGrayscale(img image.Image, w, h int) *image.Gray {
	dst := image.NewGray(image.Rect(0, 0, w, h))
	srcBounds := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx := srcBounds.Min.X + (x*(srcBounds.Dx()))/w
			sy := srcBounds.Min.Y + (y*(srcBounds.Dy()))/h
			c := img.At(sx, sy)
			dst.Set(x, y, c)
		}
	}
	return dst
}

// dct2D computes the 2D Discrete Cosine Transform for an mxn bloc using DCT-II.
func dct2D(input *[pHashHeight][pHashWidth]float64, output *[pHashHeight][pHashWidth]float64) {
	const N = pHashWidth
	const M = pHashHeight
	for u := 0; u < M; u++ {
		for v := 0; v < N; v++ {
			var sum float64
			for x := 0; x < M; x++ {
				for y := 0; y < N; y++ {
					cos1 := math.Cos((float64(2*x+1)*float64(u)*math.Pi)/float64(2*M))
					cos2 := math.Cos((float64(2*y+1)*float64(v)*math.Pi)/float64(2*N))
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

// medianFloat64 computes the median of a slice of float64 numbers.
func medianFloat64(nums []float64) float64 {
	n := len(nums)
	cp := make([]float64, n)
	copy(cp, nums)
	sort.Float64s(cp)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

func hammingDistance(a, b pHashResult) int {
    dist := 0
    for i := 0; i < len(a); i++ {
        dist += bits.OnesCount8(a[i] ^ b[i])
    }
    return dist
}
