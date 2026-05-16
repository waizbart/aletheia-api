package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
)

// trainPCA extracts MobileViT features from all tiles of a reference image,
// computes PCA to find the top K=LSH_BITS projection directions, and saves
// them as pca_planes.bin for use as LSH projection planes.
func trainPCA(imagePath string) error {
	if !fileExists(imagePath) {
		return fmt.Errorf("image not found: %s", imagePath)
	}

	log.Printf("[PCA] Extracting features from %s", imagePath)

	file, err := os.Open(imagePath)
	if err != nil {
		return err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return err
	}

	stdImg := resizeProportional(img)

	features := collectFeatures(stdImg)
	if len(features) == 0 {
		return fmt.Errorf("no features extracted")
	}

	log.Printf("[PCA] Extracted %d feature vectors (each %d-dim)", len(features), len(features[0]))

	planes := computePCA(features, LSH_BITS)
	if planes == nil {
		return fmt.Errorf("PCA computation failed")
	}

	if err := savePCAPlanes("pca_planes.bin", LSH_BITS, planes); err != nil {
		return err
	}

	log.Printf("[PCA] Saved %d PCA components to pca_planes.bin (%d bytes)", LSH_BITS, 8+LSH_BITS*1000*4)
	return nil
}

func collectFeatures(img image.Image) [][]float32 {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	var mu sync.Mutex
	var wg sync.WaitGroup
	var features [][]float32

	for y := 0; y <= height-TILE_SIZE; y += TILE_SIZE {
		for x := 0; x <= width-TILE_SIZE; x += TILE_SIZE {
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				tile := cropImage(img, tx, ty, TILE_SIZE, TILE_SIZE)
				inputTensor, err := preprocessForModel(tile)
				if err != nil {
					return
				}
				fv, err := remoteInference(inputTensor)
				if err != nil {
					return
				}
				mu.Lock()
				features = append(features, fv)
				mu.Unlock()
			}(x, y)
		}
	}

	wg.Wait()
	return features
}

func computePCA(features [][]float32, K int) [][]float32 {
	N := len(features)
	if N < 2 {
		return nil
	}
	D := len(features[0])

	mean := make([]float64, D)
	for _, fv := range features {
		for j, v := range fv {
			mean[j] += float64(v)
		}
	}
	for j := range mean {
		mean[j] /= float64(N)
	}

	centered := make([][]float64, N)
	for i, fv := range features {
		centered[i] = make([]float64, D)
		for j, v := range fv {
			centered[i][j] = float64(v) - mean[j]
		}
	}

	planes := make([][]float32, K)

	for k := 0; k < K; k++ {
		eig := make([]float64, D)
		for j := range eig {
			eig[j] = rand.NormFloat64()
		}
		for iter := 0; iter < 20; iter++ {
			proj := make([]float64, N)
			for i := 0; i < N; i++ {
				var dot float64
				for j := 0; j < D; j++ {
					dot += centered[i][j] * eig[j]
				}
				proj[i] = dot
			}

			newEig := make([]float64, D)
			for j := 0; j < D; j++ {
				var sum float64
				for i := 0; i < N; i++ {
					sum += centered[i][j] * proj[i]
				}
				newEig[j] = sum
			}

			for p := 0; p < k; p++ {
				var dot float64
				for j := 0; j < D; j++ {
					dot += newEig[j] * float64(planes[p][j])
				}
				for j := 0; j < D; j++ {
					newEig[j] -= dot * float64(planes[p][j])
				}
			}

			var norm float64
			for j := 0; j < D; j++ {
				norm += newEig[j] * newEig[j]
			}
			norm = math.Sqrt(norm)
			if norm < 1e-10 {
				break
			}
			for j := 0; j < D; j++ {
				eig[j] = newEig[j] / norm
			}
		}

		plane := make([]float32, D)
		var norm float64
		for j := 0; j < D; j++ {
			plane[j
