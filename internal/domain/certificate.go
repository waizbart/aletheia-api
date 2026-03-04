package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"io"
	"time"
)

type Certificate struct {
	ID             string
	ContentHash    string
	PerceptualHash *uint64
	Registrant     string
	TxHash         string
	BlockNumber    uint64
	CreatedAt      time.Time
}

func HashContent(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hashing content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func PerceptualHashFromBytes(content []byte) (*uint64, error) {
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, nil
	}

	gray := downsampleTo8x8Gray(img)
	var sum uint64
	for i := range gray.Pix {
		sum += uint64(gray.Pix[i])
	}
	avg := uint8(sum / 64)

	var hash uint64
	for i := range gray.Pix {
		hash <<= 1
		if gray.Pix[i] >= avg {
			hash |= 1
		}
	}

	return &hash, nil
}

func downsampleTo8x8Gray(img image.Image) *image.Gray {
	const size = 8
	bounds := img.Bounds()
	gray := image.NewGray(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		sy := bounds.Min.Y + (y*(bounds.Dy()-1))/7
		for x := 0; x < size; x++ {
			sx := bounds.Min.X + (x*(bounds.Dx()-1))/7
			gray.Set(x, y, color.GrayModel.Convert(img.At(sx, sy)))
		}
	}
	return gray
}

func HammingDistance(a, b uint64) int {
	v := a ^ b
	d := 0
	for v != 0 {
		d++
		v &= v - 1
	}
	return d
}
