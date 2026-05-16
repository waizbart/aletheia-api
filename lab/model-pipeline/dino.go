package main

import (
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/waizbart/aletheia-api/lab/model-pipeline/metrics"
)

// aggregatePatchTokensToGrid agrega tokens de patch ViT (grelha patchGrid×patchGrid, C=hidden)
// numa grelha outGrid×outGrid por média espacial (bins com fronteiras inteiras).
func aggregatePatchTokensToGrid(patches []float32, patchGrid, outGrid, hidden int) []float32 {
	if patchGrid < 1 || outGrid < 1 || hidden < 1 {
		return nil
	}
	nPatch := patchGrid * patchGrid
	want := nPatch * hidden
	if len(patches) < want {
		return nil
	}
	out := make([]float32, outGrid*outGrid*hidden)
	for oy := 0; oy < outGrid; oy++ {
		y0 := oy * patchGrid / outGrid
		y1 := (oy + 1) * patchGrid / outGrid
		for ox := 0; ox < outGrid; ox++ {
			x0 := ox * patchGrid / outGrid
			x1 := (ox + 1) * patchGrid / outGrid
			base := (oy*outGrid + ox) * hidden
			for c := 0; c < hidden; c++ {
				var sum float64
				n := 0
				for py := y0; py < y1; py++ {
					for px := x0; px < x1; px++ {
						pidx := (py*patchGrid+px)*hidden + c
						sum += float64(patches[pidx])
						n++
					}
				}
				if n > 0 {
					out[base+c] = float32(sum / float64(n))
				}
			}
		}
	}
	return out
}

// computeChannelThresholdsFromGrid calcula um limiar por canal (média sobre células espaciais).
func computeChannelThresholdsFromGrid(grid []float32, numCells, hidden int) []float32 {
	th := make([]float32, hidden)
	if numCells < 1 || hidden < 1 {
		return th
	}
	for c := 0; c < hidden; c++ {
		var sum float64
		for cell := 0; cell < numCells; cell++ {
			sum += float64(grid[cell*hidden+c])
		}
		th[c] = float32(sum / float64(numCells))
	}
	return th
}

// hammingSubBits compara nBits consecutivos a partir de startBit (LSB-first por byte, como binarizeVector).
func hammingSubBits(a, b []byte, startBit, nBits int) int {
	if nBits < 0 {
		return -1
	}
	dist := 0
	for i := 0; i < nBits; i++ {
		bit := startBit + i
		bi := bit / 8
		bo := uint(bit % 8)
		if bi >= len(a) || bi >= len(b) {
			return -1
		}
		va := (a[bi] >> bo) & 1
		vb := (b[bi] >> bo) & 1
		if va != vb {
			dist++
		}
	}
	return dist
}

func extractDinoBatchOutputs(data []float32, batchSize int) ([][]float32, [][]float32, error) {
	want := batchSize * dinoNumTokens * dinoHiddenDim
	if len(data) < want {
		return nil, nil, fmt.Errorf("saída DINO curta: %d < %d (batch=%d)", len(data), want, batchSize)
	}
	stride := dinoNumTokens * dinoHiddenDim
	clses := make([][]float32, batchSize)
	grids := make([][]float32, batchSize)
	for b := 0; b < batchSize; b++ {
		off := b * stride
		slice := data[off : off+stride]
		cls := make([]float32, dinoHiddenDim)
		copy(cls, slice[:dinoHiddenDim])
		patches := slice[dinoHiddenDim : dinoHiddenDim+dinoNumPatches*dinoHiddenDim]
		grid := aggregatePatchTokensToGrid(patches, dinoPatchGrid, DinoLocalGridSize, dinoHiddenDim)
		if grid == nil {
			return nil, nil, fmt.Errorf("agregação patch→grelha falhou (batch=%d)", b)
		}
		clses[b] = cls
		grids[b] = grid
	}
	return clses, grids, nil
}

// runDinoInferenceForTiles executa DINO: um batch único ou vários workers ORT em paralelo.
func runDinoInferenceForTiles(p *Pipeline, tiles []*image.NRGBA) ([][]float32, [][]float32, error) {
	B := len(tiles)
	if B == 0 {
		return nil, nil, fmt.Errorf("sem tiles DINO")
	}
	if len(p.DinoShards) == 1 {
		sh := p.DinoShards[0]
		if sh.batch != B {
			return nil, nil, fmt.Errorf("batch ORT %d != tiles %d", sh.batch, B)
		}
		FillDinoBatchInputParallel(tiles, sh.in.GetData())
		if err := sh.sess.Run(); err != nil {
			return nil, nil, fmt.Errorf("inferência DINOv2: %w", err)
		}
		return extractDinoBatchOutputs(sh.out.GetData(), B)
	}

	clses := make([][]float32, B)
	grids := make([][]float32, B)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for wi := range p.DinoShards {
		sh := p.DinoShards[wi]
		ch := p.DinoChunks[wi]
		part := tiles[ch.Start : ch.Start+ch.Count]
		if len(part) != sh.batch {
			return nil, nil, fmt.Errorf("shard %d: batch %d != part %d", wi, sh.batch, len(part))
		}
		off := ch.Start
		wg.Add(1)
		go func(part []*image.NRGBA, sh dinoShard, off int) {
			defer wg.Done()
			FillDinoBatchInputParallel(part, sh.in.GetData())
			if err := sh.sess.Run(); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("DINO worker: %w", err)
				}
				errMu.Unlock()
				return
			}
			sc, sg, err := extractDinoBatchOutputs(sh.out.GetData(), len(part))
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			mu.Lock()
			for i := range sc {
				clses[off+i] = sc[i]
				grids[off+i] = sg[i]
			}
			mu.Unlock()
		}(part, sh, off)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, nil, firstErr
	}
	return clses, grids, nil
}

func evaluateDinoTriple(ref *ImageHashes, img image.Image, p *Pipeline) (LayerResult, metrics.StageMetrics) {
	before := metrics.Snapshot()
	start := time.Now()

	tiles, err := ExtractDinoTiles(img, p.DinoTileRows, p.DinoTileCols)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
	}
	if len(tiles) != len(ref.DinoTiles) {
		return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
	}

	clses, grids, err := runDinoInferenceForTiles(p, tiles)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
	}

	minG, minAgg, minCell := 1.0, 1.0, 1.0
	worstTile := 0
	worstCell := 0
	worstTriple := 1.01

	for b := range clses {
		refT := &ref.DinoTiles[b]
		globalHash := binarizeVector(clses[b], refT.DinoCLSThreshold)
		globalDist := hammingBits(refT.DinoGlobalHash, globalHash)
		if globalDist < 0 {
			return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
		}
		simG := similarityFromHamming(globalDist, dinoHiddenDim)

		localCandHash := binarizeSpatial(grids[b], refT.DinoLocalThresholds)
		localDist := hammingBits(refT.DinoLocalHash, localCandHash)
		if localDist < 0 {
			return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
		}
		simAgg := similarityFromHamming(localDist, dinoLocalBits)

		simMin := 1.0
		wCell := 0
		for cell := 0; cell < dinoLocalCells; cell++ {
			startBit := cell * dinoHiddenDim
			d := hammingSubBits(refT.DinoLocalHash, localCandHash, startBit, dinoHiddenDim)
			simCell := similarityFromHamming(d, dinoHiddenDim)
			if simCell < simMin {
				simMin = simCell
				wCell = cell
			}
		}

		if simG < minG {
			minG = simG
		}
		if simAgg < minAgg {
			minAgg = simAgg
		}
		if simMin < minCell {
			minCell = simMin
		}
		triple := simG
		if simAgg < triple {
			triple = simAgg
		}
		if simMin < triple {
			triple = simMin
		}
		if triple < worstTriple {
			worstTriple = triple
			worstTile = b
			worstCell = wCell
		}
	}

	passed := minG >= ThresholdDinoGlobal &&
		minAgg >= ThresholdDinoLocalAgg &&
		minCell >= ThresholdDinoLocalMin

	combined := minG
	if minAgg < combined {
		combined = minAgg
	}
	if minCell < combined {
		combined = minCell
	}

	dur := time.Since(start)
	after := metrics.Snapshot()
	sm := before.Diff(after, dur)

	return LayerResult{
		Similarity:    combined,
		Passed:        passed,
		SimGlobal:     minG,
		SimLocalAgg:   minAgg,
		SimLocalMin:   minCell,
		WorstDinoCell: worstCell,
		WorstDinoTile: worstTile,
		Metrics:       sm,
	}, sm
}
