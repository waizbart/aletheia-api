package domain_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// testPattern builds a textured image. A flat colour would hash to a degenerate
// value, so the pattern carries enough spatial variation for the DCT to be
// meaningful. It is deliberately blocky and limited to 25 distinct colours so
// GIF's 256-entry palette encodes it without quantization loss — otherwise the
// test would measure palette error rather than decoder coverage.
func testPattern(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			bx := (x / 8) % 5
			by := (y / 8) % 5
			img.Set(x, y, color.RGBA{
				R: uint8(30 + bx*50),
				G: uint8(60 + by*40),
				B: uint8(100 + ((bx+by)%5)*25),
				A: 255,
			})
		}
	}
	return img
}

// TestPHash256_DecodesEveryAcceptedFormat pins the decoder set the pHash must
// cover. The feature extractor decodes through OpenCV, which handles BMP, TIFF
// and WebP, so the API accepts them; image.Decode only knows the formats whose
// packages are registered. When they drift apart, such an upload gets a nil
// pHash and verification answers "imagem não decodificável" for an image the
// matcher would have matched — which is exactly what BMP and TIFF did before
// golang.org/x/image was wired in.
func TestPHash256_DecodesEveryAcceptedFormat(t *testing.T) {
	const size = 128
	img := testPattern(size)

	encoders := []struct {
		format string
		encode func(*bytes.Buffer, image.Image) error
		// lossless marks formats that store the pixels exactly, so their hash
		// must land close to the PNG anchor. JPEG subsamples chroma and
		// gif.Encode snaps to the fixed 256-entry Plan9 palette; asserting on
		// their hash distance would measure encoder fidelity instead of the
		// decoder coverage this test pins.
		lossless bool
	}{
		{"png", func(b *bytes.Buffer, m image.Image) error { return png.Encode(b, m) }, true},
		{"bmp", func(b *bytes.Buffer, m image.Image) error { return bmp.Encode(b, m) }, true},
		{"tiff", func(b *bytes.Buffer, m image.Image) error { return tiff.Encode(b, m, nil) }, true},
		{"jpeg", func(b *bytes.Buffer, m image.Image) error {
			return jpeg.Encode(b, m, &jpeg.Options{Quality: 90})
		}, false},
		{"gif", func(b *bytes.Buffer, m image.Image) error { return gif.Encode(b, m, nil) }, false},
	}

	// PNG is the anchor: it is lossless, so every other lossless encoding of the
	// same pixels must hash within MaxPHashDistance of it.
	var ref *[32]byte
	for _, e := range encoders {
		t.Run(e.format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.encode(&buf, img); err != nil {
				t.Fatalf("encode %s: %v", e.format, err)
			}

			h := domain.PHash256(buf.Bytes())
			if h == nil {
				t.Fatalf("PHash256 returned nil for %s — decoder not registered", e.format)
			}
			if vs := domain.PHash256Variants(buf.Bytes()); len(vs) != 4 {
				t.Fatalf("PHash256Variants(%s) = %d variants, want 4", e.format, len(vs))
			}

			if !e.lossless {
				return
			}
			if ref == nil {
				ref = h
				return
			}
			if d := domain.Hamming256(*ref, *h); d > domain.MaxPHashDistance {
				t.Errorf("%s hash is %d bits from the png hash of the same pixels (max %d)",
					e.format, d, domain.MaxPHashDistance)
			}
		})
	}
}

// TestPHash256_WebPDecoderRegistered covers the one accepted format x/image can
// decode but not encode, so there is no round trip to assert on. Feeding a
// truncated WebP separates "format unknown" (decoder missing) from a decoder
// that is present and rejects a malformed payload.
func TestPHash256_WebPDecoderRegistered(t *testing.T) {
	// RIFF container declaring WEBP/VP8L, with the bitstream cut short.
	truncated := []byte{
		'R', 'I', 'F', 'F',
		0x14, 0x00, 0x00, 0x00,
		'W', 'E', 'B', 'P',
		'V', 'P', '8', 'L',
		0x08, 0x00, 0x00, 0x00,
		0x2f, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}

	_, _, err := image.Decode(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected a decode error for a truncated WebP")
	}
	if err == image.ErrFormat {
		t.Fatal("image.Decode reports unknown format — the WebP decoder is not registered")
	}

	// A nil pHash is the correct answer for undecodable bytes.
	if h := domain.PHash256(truncated); h != nil {
		t.Errorf("PHash256(truncated webp) = %x, want nil", *h)
	}
}
