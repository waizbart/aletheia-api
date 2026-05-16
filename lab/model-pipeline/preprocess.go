package main

import (
	"image"
	"math"
	"sync"

	"github.com/disintegration/imaging"
)

// preprocessImagenetNCHWInto escreve no tensor de batch NCHW no slot batchIdx
// (plane = H*W). Layout: out[batchIdx*3*plane + c*plane + y*W + x].
func preprocessImagenetNCHWInto(nrgba *image.NRGBA, size int, out []float32, batchIdx, plane int) {
	bounds := nrgba.Bounds()
	if bounds.Dx() != size || bounds.Dy() != size {
		panic("preprocessImagenetNCHWInto: dimensões inesperadas")
	}
	base := batchIdx * 3 * plane
	mean := [3]float32{0.485, 0.456, 0.406}
	std := [3]float32{0.229, 0.224, 0.225}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := nrgba.PixOffset(bounds.Min.X+x, bounds.Min.Y+y)
			fr := float32(nrgba.Pix[i]) / 255.0
			fg := float32(nrgba.Pix[i+1]) / 255.0
			fb := float32(nrgba.Pix[i+2]) / 255.0
			idx := y*size + x
			out[base+idx] = (fr - mean[0]) / std[0]
			out[base+idx+plane] = (fg - mean[1]) / std[1]
			out[base+idx+2*plane] = (fb - mean[2]) / std[2]
		}
	}
}

// preprocessImagenetNCHW preenche tensor NCHW [1,3,size,size] a partir de *image.NRGBA
// (RGB 8-bit), normalização ImageNet mean/std.
func preprocessImagenetNCHW(nrgba *image.NRGBA, size int, out []float32) {
	plane := size * size
	preprocessImagenetNCHWInto(nrgba, size, out, 0, plane)
}

// FillDinoBatchInputParallel preenche [B,3,H,W] com um tile por slot (goroutines).
func FillDinoBatchInputParallel(tiles []*image.NRGBA, tensor []float32) {
	plane := DinoInputSize * DinoInputSize
	var wg sync.WaitGroup
	for b := range tiles {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			preprocessImagenetNCHWInto(tiles[idx], DinoInputSize, tensor, idx, plane)
		}(b)
	}
	wg.Wait()
}

func preprocessDino(img image.Image) []float32 {
	resized := imaging.Resize(img, DinoInputSize, DinoInputSize, imaging.Lanczos)
	nrgba := imageToRGBA(resized)
	out := make([]float32, 3*DinoInputSize*DinoInputSize)
	preprocessImagenetNCHW(nrgba, DinoInputSize, out)
	return out
}

// fillRotNetInputNHWC preenche [1,224,224,3] float32 NHWC com pré-processamento
// equivalente ao mode='caffe' do Keras (BGR mean subtraction).
func fillRotNetInputNHWC(nrgba *image.NRGBA, out []float32) {
	bounds := nrgba.Bounds()
	if bounds.Dx() != RotNetInputSize || bounds.Dy() != RotNetInputSize {
		panic("fillRotNetInputNHWC: esperado 224x224")
	}
	const (
		meanB = 103.939
		meanG = 116.779
		meanR = 123.68
	)
	idx := 0
	for y := 0; y < RotNetInputSize; y++ {
		for x := 0; x < RotNetInputSize; x++ {
			i := nrgba.PixOffset(bounds.Min.X+x, bounds.Min.Y+y)
			r := float32(nrgba.Pix[i])
			g := float32(nrgba.Pix[i+1])
			b := float32(nrgba.Pix[i+2])
			out[idx] = b - meanB
			out[idx+1] = g - meanG
			out[idx+2] = r - meanR
			idx += 3
		}
	}
}

func argmax360(v []float32) int {
	best := 0
	var max float32 = -math.MaxFloat32
	for i := range v {
		if v[i] > max {
			max = v[i]
			best = i
		}
	}
	return best
}
