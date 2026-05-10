package main

import (
	"fmt"
	"image"

	"github.com/disintegration/imaging"
)

// DinoChunk é um intervalo contíguo de índices de tiles processado por um worker ORT.
type DinoChunk struct {
	Start int
	Count int
}

// DistributeDinoChunks reparte nTiles em até nWorkers partes (diferença ≤ 1).
func DistributeDinoChunks(nTiles, nWorkers int) []DinoChunk {
	if nTiles < 1 {
		return nil
	}
	if nWorkers < 1 {
		nWorkers = 1
	}
	if nWorkers > nTiles {
		nWorkers = nTiles
	}
	if nWorkers > DinoMaxWorkers {
		nWorkers = DinoMaxWorkers
	}
	out := make([]DinoChunk, nWorkers)
	base := nTiles / nWorkers
	rem := nTiles % nWorkers
	idx := 0
	for w := 0; w < nWorkers; w++ {
		cnt := base
		if w < rem {
			cnt++
		}
		out[w] = DinoChunk{Start: idx, Count: cnt}
		idx += cnt
	}
	return out
}

// ExtractDinoTiles recorta a imagem numa grelha rows×cols e redimensiona cada tile para DinoInputSize.
func ExtractDinoTiles(img image.Image, rows, cols int) ([]*image.NRGBA, error) {
	if rows < 1 || cols < 1 {
		return nil, fmt.Errorf("tiling: rows/cols >= 1")
	}
	if rows*cols > DinoMaxTiles {
		return nil, fmt.Errorf("tiling: rows*cols <= %d", DinoMaxTiles)
	}
	b := imageToRGBA(img)
	bounds := b.Bounds()
	W, H := bounds.Dx(), bounds.Dy()
	if W < 1 || H < 1 {
		return nil, fmt.Errorf("tiling: imagem vazia")
	}
	n := rows * cols
	out := make([]*image.NRGBA, n)
	tw := W / cols
	th := H / rows
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	k := 0
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			x0 := bounds.Min.X + gx*tw
			y0 := bounds.Min.Y + gy*th
			x1 := x0 + tw
			y1 := y0 + th
			if gx == cols-1 {
				x1 = bounds.Max.X
			}
			if gy == rows-1 {
				y1 = bounds.Max.Y
			}
			sub := imaging.Crop(b, image.Rect(x0, y0, x1, y1))
			out[k] = imaging.Resize(sub, DinoInputSize, DinoInputSize, imaging.Lanczos)
			k++
		}
	}
	return out, nil
}
