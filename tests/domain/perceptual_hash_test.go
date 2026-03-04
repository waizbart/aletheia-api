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

	lowHash, err := domain.PerceptualHashFromBytes(lowQ.Bytes())
	if err != nil {
		t.Fatalf("low hash error: %v", err)
	}
	highHash, err := domain.PerceptualHashFromBytes(highQ.Bytes())
	if err != nil {
		t.Fatalf("high hash error: %v", err)
	}
	if lowHash == nil || highHash == nil {
		t.Fatal("expected perceptual hashes for both jpeg encodings")
	}

	d := domain.HammingDistance(*lowHash, *highHash)
	if d > 8 {
		t.Fatalf("distance too large for same image: got %d", d)
	}
}

func TestPerceptualHashFromBytes_NonImage(t *testing.T) {
	h, err := domain.PerceptualHashFromBytes([]byte("not-an-image"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != nil {
		t.Fatal("expected nil hash for non-image bytes")
	}
}
