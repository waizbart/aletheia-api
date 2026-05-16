package main

import (
	"image"
	"testing"
)

func TestAggregatePatchTokensToGrid_2x2(t *testing.T) {
	t.Parallel()
	patches := []float32{
		1, 10, 2, 20,
		3, 30, 4, 40,
	}
	out := aggregatePatchTokensToGrid(patches, 2, 1, 2)
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0] != 2.5 || out[1] != 25 {
		t.Fatalf("got %v want [2.5,25]", out)
	}
}

func TestHammingSubBits(t *testing.T) {
	t.Parallel()
	a := []byte{0b10101010}
	b := []byte{0b10001000}
	d := hammingSubBits(a, b, 1, 3)
	if d != 1 {
		t.Fatalf("dist=%d", d)
	}
}

func TestPickModelPath(t *testing.T) {
	t.Parallel()
	if pickModelPath("models/x.onnx", false) != "models/x.onnx" {
		t.Fatal()
	}
}

func TestDistributeDinoChunks(t *testing.T) {
	t.Parallel()
	ch := DistributeDinoChunks(7, 3)
	if len(ch) != 3 {
		t.Fatalf("workers=%d", len(ch))
	}
	sum := 0
	for _, c := range ch {
		sum += c.Count
		if c.Start < 0 || c.Count < 1 {
			t.Fatalf("chunk inválido %+v", c)
		}
	}
	if sum != 7 {
		t.Fatalf("sum counts=%d", sum)
	}
	if ch[0].Count != 3 || ch[1].Count != 2 || ch[2].Count != 2 {
		t.Fatalf("esperado [3,2,2] counts, got %+v", ch)
	}
	if ch[0].Start != 0 || ch[1].Start != 3 || ch[2].Start != 5 {
		t.Fatalf("starts errados %+v", ch)
	}

	ch2 := DistributeDinoChunks(5, 2)
	if len(ch2) != 2 || ch2[0].Count+ch2[1].Count != 5 {
		t.Fatalf("5/2 chunks %+v", ch2)
	}
}

func TestExtractDinoTiles1x1(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 100, 80))
	tiles, err := ExtractDinoTiles(img, 1, 1)
	if err != nil || len(tiles) != 1 {
		t.Fatal(err, len(tiles))
	}
	if tiles[0].Bounds().Dx() != DinoInputSize {
		t.Fatalf("got %d", tiles[0].Bounds().Dx())
	}
}
