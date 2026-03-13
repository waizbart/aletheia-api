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
)

const ORIGINAL_FILE_NAME = "aletheia.jpg"
const MIN_H_DIST = 6

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

// dHashSize defines the width (w) and height (h) for the dHash algorithm.
// The typical size is 9x8. This provides an 8x8 hash.
const (
	dHashWidth  = 9
	dHashHeight = 8
)

// dHashResult returns a 64-bit hash as a fixed-length array.
type dHashResult [8]byte

// hashFile computes the dHash (difference hash) of an image file.
func hashFile(path string) (dHashResult, error) {
	var empty dHashResult

	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return empty, err
	}

	// Resize the image to dHashWidth x dHashHeight and convert to grayscale
	gray := resizeAndGrayscale(img, dHashWidth, dHashHeight)

	var hash uint64 = 0
	bit := 0

	// dHash: For each row, compare adjacent pixels left-to-right
	for y := 0; y < dHashHeight; y++ {
		for x := 0; x < dHashWidth-1; x++ {
			left := gray.GrayAt(x, y).Y
			right := gray.GrayAt(x+1, y).Y
			if left > right {
				hash |= 1 << (63 - bit)
			}
			bit++
		}
	}

	// Convert uint64 to [8]byte
	var result dHashResult
	for i := 0; i < 8; i++ {
		result[i] = byte(hash >> (56 - 8*i))
	}

	return result, nil
}

// resizeAndGrayscale resizes img to w x h and converts to *image.Gray
func resizeAndGrayscale(img image.Image, w, h int) *image.Gray {
	dst := image.NewGray(image.Rect(0, 0, w, h))
	// Nearest neighbor downsample
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

func hammingDistance(a, b dHashResult) int {
    dist := 0
    for i := 0; i < len(a); i++ {
        dist += bits.OnesCount8(a[i] ^ b[i])
    }
    return dist
}
