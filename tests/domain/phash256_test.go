package domain_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestPHash256_StableAcrossCompression(t *testing.T) {
	const size = 256
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			band := (x / 16) % 4
			row := (y / 16) % 4
			r := uint8(50 + band*40)
			g := uint8(80 + row*30)
			b := uint8(120 + ((x ^ y) % 32))
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
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

	low := domain.PHash256(lowQ.Bytes())
	high := domain.PHash256(highQ.Bytes())
	if low == nil || high == nil {
		t.Fatal("expected non-nil hashes")
	}

	d := domain.Hamming256(*low, *high)
	if d > domain.MaxPHashDistance {
		t.Fatalf("distance too large for same image: got %d (max %d)", d, domain.MaxPHashDistance)
	}
}

func TestPHash256_NonImage(t *testing.T) {
	if h := domain.PHash256([]byte("not-an-image")); h != nil {
		t.Fatalf("expected nil hash for non-image bytes, got %v", h)
	}
}

func TestPHash256Variants_Rotations(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode: %v", err)
	}

	variants := domain.PHash256Variants(buf.Bytes())
	if len(variants) != 4 {
		t.Fatalf("expected 4 variants, got %d", len(variants))
	}
	for i := 1; i < 4; i++ {
		if variants[i] == variants[0] {
			t.Errorf("variant %d unexpectedly equals variant 0", i)
		}
	}
}

func TestPHash256Variants_NonImage(t *testing.T) {
	if v := domain.PHash256Variants([]byte("not-an-image")); v != nil {
		t.Fatalf("expected nil variants for non-image, got %v", v)
	}
}

func TestHamming256(t *testing.T) {
	zero := [32]byte{}
	if d := domain.Hamming256(zero, zero); d != 0 {
		t.Fatalf("zero distance = %d, want 0", d)
	}

	ones := [32]byte{}
	for i := range ones {
		ones[i] = 0xFF
	}
	if d := domain.Hamming256(zero, ones); d != 256 {
		t.Fatalf("max distance = %d, want 256", d)
	}

	a := [32]byte{0b1010}
	b := [32]byte{0b0011}
	if d := domain.Hamming256(a, b); d != 2 {
		t.Fatalf("distance = %d, want 2", d)
	}
}
