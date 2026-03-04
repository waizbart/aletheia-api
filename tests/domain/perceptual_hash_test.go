package domain_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestPerceptualHashFromBytes_IsStableAcrossCompression(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 3), B: uint8((x + y) * 2), A: 255})
		}
	}

	var lowQ bytes.Buffer
	if err := jpeg.Encode(&lowQ, img, &jpeg.Options{Quality: 40}); err != nil {
		t.Fatalf("encode low quality: %v", err)
	}

	var highQ bytes.Buffer
	if err := jpeg.Encode(&highQ, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode high quality: %v", err)
	}

	lowHash := domain.PerceptualHashFromBytes(lowQ.Bytes())
	highHash := domain.PerceptualHashFromBytes(highQ.Bytes())
	if lowHash == nil || highHash == nil {
		t.Fatal("expected perceptual hashes for both jpeg encodings")
	}

	d := domain.HammingDistance(*lowHash, *highHash)
	if d > 8 {
		t.Fatalf("distance too large for same image: got %d", d)
	}
}

func TestPerceptualHashFromBytes_NonImage(t *testing.T) {
	h := domain.PerceptualHashFromBytes([]byte("not-an-image"))
	if h != nil {
		t.Fatal("expected nil hash for non-image bytes")
	}
}

func TestHammingDistance_ZeroAndDifferent(t *testing.T) {
	if got := domain.HammingDistance(0, 0); got != 0 {
		t.Fatalf("zero distance = %d, want 0", got)
	}
	if got := domain.HammingDistance(0b1010, 0b0011); got != 2 {
		t.Fatalf("distance = %d, want 2", got)
	}
}
