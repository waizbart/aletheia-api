package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/corona10/goimagehash"
	"github.com/disintegration/imaging"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/waizbart/aletheia-api/lab/model-pipeline/metrics"
)

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

type LayerResult struct {
	Similarity float64
	Passed     bool
	Skipped    bool
	Hamming    int `json:"hamming"`

	SimGlobal     float64 `json:"sim_global,omitempty"`
	SimLocalAgg   float64 `json:"sim_local_agg,omitempty"`
	SimLocalMin   float64 `json:"sim_local_min,omitempty"`
	WorstDinoCell int     `json:"worst_dino_cell,omitempty"`
	WorstDinoTile int     `json:"worst_dino_tile,omitempty"`

	Metrics metrics.StageMetrics `json:"metrics"`
}

// DinoTileHashes são os hashes DINO de um tile da grelha.
type DinoTileHashes struct {
	DinoCLSThreshold    float32
	DinoGlobalHash      []byte
	DinoLocalThresholds []float32
	DinoLocalHash       []byte
}

type ImageHashes struct {
	PHash *goimagehash.ImageHash

	DinoTiles []DinoTileHashes

	ColorThresholds [6]float32
	ColorHash       []byte
}

type dinoShard struct {
	batch int
	sess  *ort.AdvancedSession
	in    *ort.Tensor[float32]
	out   *ort.Tensor[float32]
}

type Result struct {
	ImagePath     string
	RotNetSkipped bool
	RotNetDegrees int
	L0            LayerResult
	L1            LayerResult
	L2            LayerResult
	L4            LayerResult
	PathType      string
	Authentic     bool
	Error         string
}

func (r Result) PerImageMetrics() metrics.PerImageMetrics {
	name := filepath.Base(r.ImagePath)
	return metrics.PerImageMetrics{
		ImageName: name,
		Authentic: r.Authentic,
		PathType:  r.PathType,
		Stages: map[string]metrics.StageMetrics{
			"L0 (RotNet)": r.L0.Metrics,
			"L1 (pHash)":  r.L1.Metrics,
			"L2 (DINOv2)": r.L2.Metrics,
			"L4 (Cores)":  r.L4.Metrics,
		},
		Total: metrics.DiffTotal(map[string]metrics.StageMetrics{
			"L0 (RotNet)": r.L0.Metrics,
			"L1 (pHash)":  r.L1.Metrics,
			"L2 (DINOv2)": r.L2.Metrics,
			"L4 (Cores)":  r.L4.Metrics,
		}),
	}
}

type Pipeline struct {
	sessionOptions *ort.SessionOptions

	DinoShards   []dinoShard
	DinoChunks   []DinoChunk
	DinoTileRows int
	DinoTileCols int
	DinoWorkers  int

	RotNetSession *ort.AdvancedSession
	RotNetInput   *ort.Tensor[float32]
	RotNetOutput  *ort.Tensor[float32]

	Ref *ImageHashes
}

// ---------------------------------------------------------------------------
// Utilitários
// ---------------------------------------------------------------------------

func hammingBits(a, b []byte) int {
	if len(a) != len(b) {
		return -1
	}
	dist := 0
	for i := range a {
		dist += bits.OnesCount8(a[i] ^ b[i])
	}
	return dist
}

func similarityFromHamming(dist, totalBits int) float64 {
	if totalBits == 0 {
		return 0
	}
	return 1.0 - float64(dist)/float64(totalBits)
}

func loadImage(path string) (image.Image, error) {
	img, err := imaging.Open(path)
	if err != nil {
		return nil, fmt.Errorf("falha ao abrir imagem %s: %w", path, err)
	}
	return img, nil
}

func imageToRGBA(img image.Image) *image.NRGBA {
	switch v := img.(type) {
	case *image.NRGBA:
		return v
	default:
		return imaging.Clone(img)
	}
}

func pickModelPath(fp32Path string, preferInt8 bool) string {
	if !preferInt8 {
		return fp32Path
	}
	if strings.HasSuffix(fp32Path, ".int8.onnx") {
		return fp32Path
	}
	if !strings.HasSuffix(fp32Path, ".onnx") {
		return fp32Path
	}
	int8p := strings.TrimSuffix(fp32Path, ".onnx") + ".int8.onnx"
	if _, err := os.Stat(int8p); err == nil {
		return int8p
	}
	return fp32Path
}

func loadRotNetIONames(manifestPath, overrideIn, overrideOut string) (string, string, error) {
	inName, outName := "input", "fc360"
	if overrideIn != "" {
		inName = overrideIn
	}
	if overrideOut != "" {
		outName = overrideOut
	}
	if overrideIn != "" && overrideOut != "" {
		return inName, outName, nil
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return inName, outName, nil
	}
	var m struct {
		Input  string `json:"input"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", err
	}
	if m.Input != "" && overrideIn == "" {
		inName = m.Input
	}
	if m.Output != "" && overrideOut == "" {
		outName = m.Output
	}
	return inName, outName, nil
}

// parseDinoTilesStr interpreta "RxC" (ex.: 2x2). Default 1x1.
func parseDinoTilesStr(s string) (rows, cols int, err error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "1x1" {
		return 1, 1, nil
	}
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("-dino-tiles: use formato RxC, ex. 2x2")
	}
	r, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || r < 1 {
		return 0, 0, fmt.Errorf("-dino-tiles: rows inválido")
	}
	c, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || c < 1 {
		return 0, 0, fmt.Errorf("-dino-tiles: cols inválido")
	}
	if r*c > DinoMaxTiles {
		return 0, 0, fmt.Errorf("-dino-tiles: RxC <= %d", DinoMaxTiles)
	}
	return r, c, nil
}

func openDinoShard(dinoPath string, batch int, opts *ort.SessionOptions) (dinoShard, error) {
	if batch < 1 {
		return dinoShard{}, fmt.Errorf("batch DINO < 1")
	}
	inShape := ort.NewShape(int64(batch), int64(3), int64(DinoInputSize), int64(DinoInputSize))
	outShape := ort.NewShape(int64(batch), int64(dinoNumTokens), int64(dinoHiddenDim))
	sess, in, out, err := openOnnxSessionAdvanced(
		dinoPath,
		[]string{"pixel_values"},
		[]string{"last_hidden_state"},
		inShape, outShape, opts,
	)
	if err != nil {
		return dinoShard{}, err
	}
	return dinoShard{batch: batch, sess: sess, in: in, out: out}, nil
}

// ---------------------------------------------------------------------------
// L1 — pHash
// ---------------------------------------------------------------------------

func computePHash(img image.Image) (*goimagehash.ImageHash, error) {
	return goimagehash.PerceptionHash(img)
}

func phashSimilarity(ref, cand *goimagehash.ImageHash) (float64, int, error) {
	dist, err := ref.Distance(cand)
	if err != nil {
		return 0, 0, err
	}
	sim := similarityFromHamming(dist, 64)
	return sim, dist, nil
}

// ---------------------------------------------------------------------------
// DINO — binarização (CLS + local compartilhado com dino.go)
// ---------------------------------------------------------------------------

func binarizeVector(vec []float32, threshold float32) []byte {
	numBits := len(vec)
	numBytes := (numBits + 7) / 8
	result := make([]byte, numBytes)
	for i, v := range vec {
		if v >= threshold {
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			result[byteIdx] |= 1 << bitIdx
		}
	}
	return result
}

func computeThreshold(vec []float32) float32 {
	if len(vec) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vec {
		sum += float64(v)
	}
	return float32(sum / float64(len(vec)))
}

func binarizeSpatial(features, thresholds []float32) []byte {
	numBits := len(features)
	numBytes := (numBits + 7) / 8
	result := make([]byte, numBytes)
	channels := len(thresholds)
	for i, v := range features {
		c := i % channels
		if v >= thresholds[c] {
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			result[byteIdx] |= 1 << bitIdx
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// RotNet L0
// ---------------------------------------------------------------------------

func applyCanonicalRotation(img image.Image, p *Pipeline) (image.Image, int, error) {
	if p.RotNetSession == nil {
		return img, 0, nil
	}
	small := imaging.Resize(img, RotNetInputSize, RotNetInputSize, imaging.Linear)
	nrgba := imageToRGBA(small)
	fillRotNetInputNHWC(nrgba, p.RotNetInput.GetData())
	if err := p.RotNetSession.Run(); err != nil {
		return nil, 0, fmt.Errorf("RotNet: %w", err)
	}
	logits := p.RotNetOutput.GetData()
	if len(logits) < 360 {
		return nil, 0, fmt.Errorf("saída RotNet inesperada: len=%d", len(logits))
	}
	angle := argmax360(logits)
	out := imaging.Rotate(img, -float64(angle), color.NRGBA{A: 0xff})
	return out, angle, nil
}

// ---------------------------------------------------------------------------
// ONNX
// ---------------------------------------------------------------------------

func initOnnxRuntime() error {
	libPath := os.Getenv("ONNX_RUNTIME_LIB")
	if libPath == "" {
		for _, candidate := range []string{
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so",
			"libonnxruntime.so",
		} {
			if _, err := os.Stat(candidate); err == nil {
				libPath = candidate
				break
			}
		}
	}
	if libPath != "" {
		ort.SetSharedLibraryPath(libPath)
	}
	return ort.InitializeEnvironment()
}

func openOnnxSessionAdvanced(
	modelPath string,
	inputNames, outputNames []string,
	inputShape, outputShape ort.Shape,
	opts *ort.SessionOptions,
) (*ort.AdvancedSession, *ort.Tensor[float32], *ort.Tensor[float32], error) {
	inputData := make([]float32, inputShape.FlattenedSize())
	inputTensor, err := ort.NewTensor(inputShape, inputData)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("tensor entrada: %w", err)
	}
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		inputTensor.Destroy()
		return nil, nil, nil, fmt.Errorf("tensor saída: %w", err)
	}
	session, err := ort.NewAdvancedSession(
		modelPath,
		inputNames,
		outputNames,
		[]ort.Value{inputTensor},
		[]ort.Value{outputTensor},
		opts,
	)
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, nil, nil, fmt.Errorf("sessão ONNX: %w", err)
	}
	return session, inputTensor, outputTensor, nil
}

func closeOnnxSession(session *ort.AdvancedSession) {
	if session != nil {
		session.Destroy()
	}
}

func computeReferenceHashes(refImg image.Image, p *Pipeline) (*ImageHashes, error) {
	ref := &ImageHashes{}

	phash, err := computePHash(refImg)
	if err != nil {
		return nil, fmt.Errorf("pHash referência: %w", err)
	}
	ref.PHash = phash

	tiles, err := ExtractDinoTiles(refImg, p.DinoTileRows, p.DinoTileCols)
	if err != nil {
		return nil, fmt.Errorf("tiles DINO referência: %w", err)
	}
	clses, grids, err := runDinoInferenceForTiles(p, tiles)
	if err != nil {
		return nil, fmt.Errorf("DINO referência: %w", err)
	}
	ref.DinoTiles = make([]DinoTileHashes, len(clses))
	for b := range clses {
		th := computeThreshold(clses[b])
		ref.DinoTiles[b].DinoCLSThreshold = th
		ref.DinoTiles[b].DinoGlobalHash = binarizeVector(clses[b], th)
		ref.DinoTiles[b].DinoLocalThresholds = computeChannelThresholdsFromGrid(grids[b], dinoLocalCells, dinoHiddenDim)
		ref.DinoTiles[b].DinoLocalHash = binarizeSpatial(grids[b], ref.DinoTiles[b].DinoLocalThresholds)
	}

	colorVec := computeColorHash(refImg)
	ref.ColorThresholds = computeColorThresholds(colorVec)
	ref.ColorHash = binarizeColorVector(colorVec, ref.ColorThresholds)

	return ref, nil
}

// NewPipeline carrega um ou mais shards DINO (batch ou workers paralelos), RotNet opcional.
func NewPipeline(
	dinoModel, rotnetModel, refPath, rotManifestPath, rotInOverride, rotOutOverride string,
	preferInt8, skipRotNet bool,
	dinoTileRows, dinoTileCols, dinoWorkers int,
) (*Pipeline, error) {
	if err := initOnnxRuntime(); err != nil {
		return nil, err
	}

	opts, err := buildSessionOptions()
	if err != nil {
		return nil, err
	}

	dinoPath := pickModelPath(dinoModel, preferInt8)
	B := dinoTileRows * dinoTileCols
	if B < 1 || B > DinoMaxTiles {
		opts.Destroy()
		return nil, fmt.Errorf("DINO tiles: rows*cols entre 1 e %d", DinoMaxTiles)
	}

	effW := dinoWorkers
	if effW <= 1 {
		effW = 1
	} else {
		if effW > B {
			effW = B
		}
		if effW > DinoMaxWorkers {
			effW = DinoMaxWorkers
		}
	}

	chunks := DistributeDinoChunks(B, effW)
	var shards []dinoShard
	for _, ch := range chunks {
		sh, err := openDinoShard(dinoPath, ch.Count, opts)
		if err != nil {
			for _, s := range shards {
				closeOnnxSession(s.sess)
				if s.in != nil {
					s.in.Destroy()
				}
				if s.out != nil {
					s.out.Destroy()
				}
			}
			opts.Destroy()
			return nil, fmt.Errorf("DINOv2 shard batch=%d: %w", ch.Count, err)
		}
		shards = append(shards, sh)
	}

	rotInName, rotOutName, err := loadRotNetIONames(rotManifestPath, rotInOverride, rotOutOverride)
	if err != nil {
		for _, s := range shards {
			closeOnnxSession(s.sess)
			if s.in != nil {
				s.in.Destroy()
			}
			if s.out != nil {
				s.out.Destroy()
			}
		}
		opts.Destroy()
		return nil, err
	}

	pl := &Pipeline{
		sessionOptions: opts,
		DinoShards:     shards,
		DinoChunks:     chunks,
		DinoTileRows:   dinoTileRows,
		DinoTileCols:   dinoTileCols,
		DinoWorkers:    effW,
	}

	if skipRotNet {
		refImg, err := loadImage(refPath)
		if err != nil {
			pl.Close()
			return nil, fmt.Errorf("referência: %w", err)
		}
		ref, err := computeReferenceHashes(refImg, pl)
		if err != nil {
			pl.Close()
			return nil, fmt.Errorf("hashes referência: %w", err)
		}
		pl.Ref = ref
		return pl, nil
	}

	rotPath := pickModelPath(rotnetModel, preferInt8)
	rotShape := ort.NewShape(1, RotNetInputSize, RotNetInputSize, 3)
	rotOutShape := ort.NewShape(1, 360)
	rotSess, rotIn, rotOut, err := openOnnxSessionAdvanced(
		rotPath,
		[]string{rotInName},
		[]string{rotOutName},
		rotShape, rotOutShape, opts,
	)
	if err != nil {
		pl.Close()
		return nil, fmt.Errorf("RotNet: %w", err)
	}

	pl.RotNetSession = rotSess
	pl.RotNetInput = rotIn
	pl.RotNetOutput = rotOut

	refImg, err := loadImage(refPath)
	if err != nil {
		pl.Close()
		return nil, fmt.Errorf("referência: %w", err)
	}
	refCanon, _, err := applyCanonicalRotation(refImg, pl)
	if err != nil {
		pl.Close()
		return nil, fmt.Errorf("L0 referência: %w", err)
	}

	ref, err := computeReferenceHashes(refCanon, pl)
	if err != nil {
		pl.Close()
		return nil, fmt.Errorf("hashes referência: %w", err)
	}
	pl.Ref = ref
	return pl, nil
}

func (p *Pipeline) Close() {
	for i := range p.DinoShards {
		s := &p.DinoShards[i]
		closeOnnxSession(s.sess)
		s.sess = nil
		if s.in != nil {
			s.in.Destroy()
			s.in = nil
		}
		if s.out != nil {
			s.out.Destroy()
			s.out = nil
		}
	}
	p.DinoShards = nil
	p.DinoChunks = nil
	if p.RotNetSession != nil {
		p.RotNetSession.Destroy()
		p.RotNetSession = nil
	}
	if p.RotNetInput != nil {
		p.RotNetInput.Destroy()
		p.RotNetInput = nil
	}
	if p.RotNetOutput != nil {
		p.RotNetOutput.Destroy()
		p.RotNetOutput = nil
	}
	if p.sessionOptions != nil {
		p.sessionOptions.Destroy()
		p.sessionOptions = nil
	}
	ort.DestroyEnvironment()
}

// ---------------------------------------------------------------------------
// Avaliação
// ---------------------------------------------------------------------------

func evaluatePHash(ref *ImageHashes, img image.Image) (LayerResult, metrics.StageMetrics) {
	before := metrics.Snapshot()
	start := time.Now()
	candPHash, err := computePHash(img)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
	}
	sim, dist, err := phashSimilarity(ref.PHash, candPHash)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
	}
	passed := sim >= ThresholdL1Fast
	dur := time.Since(start)
	sm := before.Diff(metrics.Snapshot(), dur)
	return LayerResult{
		Similarity: sim,
		Passed:     passed,
		Hamming:    dist,
		Metrics:    sm,
	}, sm
}

func evaluateColor(ref *ImageHashes, img image.Image) (LayerResult, metrics.StageMetrics) {
	before := metrics.Snapshot()
	start := time.Now()
	colorVec := computeColorHash(img)
	candHash := binarizeColorVector(colorVec, ref.ColorThresholds)
	dist := hammingBits(ref.ColorHash, candHash)
	if dist < 0 {
		return LayerResult{Similarity: 0, Passed: false}, metrics.StageMetrics{}
	}
	sim := similarityFromHamming(dist, colorTotalBins)
	passed := sim >= ThresholdL4Full
	dur := time.Since(start)
	sm := before.Diff(metrics.Snapshot(), dur)
	return LayerResult{
		Similarity: sim,
		Passed:     passed,
		Metrics:    sm,
	}, sm
}

func evaluate(candidatePath string, p *Pipeline) Result {
	res := Result{ImagePath: candidatePath}
	img, err := loadImage(candidatePath)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	b0 := metrics.Snapshot()
	t0 := time.Now()
	if p.RotNetSession == nil {
		res.RotNetSkipped = true
		res.L0 = LayerResult{
			Similarity: 1,
			Passed:     true,
			Skipped:    true,
			Metrics:    b0.Diff(metrics.Snapshot(), time.Since(t0)),
		}
	} else {
		var angle int
		img, angle, err = applyCanonicalRotation(img, p)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.RotNetDegrees = angle
		res.L0 = LayerResult{
			Similarity: 1,
			Passed:     true,
			Hamming:    angle,
			Metrics:    b0.Diff(metrics.Snapshot(), time.Since(t0)),
		}
	}
	res.L1, _ = evaluatePHash(p.Ref, img)

	if res.L1.Passed {
		res.L2 = LayerResult{Skipped: true}
		res.L4, _ = evaluateColor(p.Ref, img)
		if res.L4.Similarity >= ThresholdL4Fast {
			res.PathType = "rápido"
			res.Authentic = true
			return res
		}
		res.L2, _ = evaluateDinoTriple(p.Ref, img, p)
		if res.L2.Passed && res.L4.Similarity >= ThresholdL4Full {
			res.PathType = "completo"
			res.Authentic = true
		} else {
			res.PathType = "completo"
			res.Authentic = false
		}
		return res
	}

	res.L2, _ = evaluateDinoTriple(p.Ref, img, p)
	res.L4, _ = evaluateColor(p.Ref, img)
	if res.L2.Passed && res.L4.Similarity >= ThresholdL4Full {
		res.PathType = "completo"
		res.Authentic = true
	} else {
		res.PathType = "completo"
		res.Authentic = false
	}
	return res
}

// ---------------------------------------------------------------------------
// Saída
// ---------------------------------------------------------------------------

func layerLabel(sim float64, passed, skipped bool, hamming int, showHamming bool) string {
	if skipped {
		return "PULADO"
	}
	if passed {
		return "PASSOU"
	}
	return "FALHOU"
}

func pct(v float64) string {
	return fmt.Sprintf("%6.2f%%", v*100)
}

func printResult(res Result, verbose bool) {
	fmt.Println("========================================")
	fmt.Printf("Imagem: %s\n", res.ImagePath)
	fmt.Println("========================================")
	if res.Error != "" {
		fmt.Printf("ERRO: %s\n", res.Error)
		fmt.Println("========================================")
		fmt.Println()
		return
	}

	if res.RotNetSkipped {
		fmt.Println("L0 (RotNet):      DESLIGADO (--skip-rotnet)")
	} else {
		fmt.Printf("L0 (RotNet):       angulo estimado %d (canonizacao aplicada)\n", res.RotNetDegrees)
	}
	if verbose {
		fmt.Printf("                   %s\n", metricsLine(res.L0.Metrics))
	}

	l1Label := layerLabel(res.L1.Similarity, res.L1.Passed, res.L1.Skipped, res.L1.Hamming, true)
	fmt.Printf("L1 (pHash):        %s  | similaridade: %s | hamming: %d",
		l1Label, pct(res.L1.Similarity), res.L1.Hamming)
	if verbose {
		fmt.Printf("  | %s", metricsLine(res.L1.Metrics))
	}
	fmt.Println()

	if !res.L2.Skipped {
		l2Label := layerLabel(res.L2.Similarity, res.L2.Passed, res.L2.Skipped, 0, false)
		fmt.Printf("L2 (DINOv2 728):   %s  | min(global,agg,min)=%s\n", l2Label, pct(res.L2.Similarity))
		fmt.Printf("                   global=%s  local_agg=%s  local_min=%s (pior tile %d célula %d)\n",
			pct(res.L2.SimGlobal), pct(res.L2.SimLocalAgg), pct(res.L2.SimLocalMin), res.L2.WorstDinoTile, res.L2.WorstDinoCell)
		if verbose {
			fmt.Printf("                   %s\n", metricsLine(res.L2.Metrics))
		}
	} else {
		fmt.Println("L2 (DINOv2):       PULADO (caminho rápido)")
	}

	l4Label := layerLabel(res.L4.Similarity, res.L4.Passed, res.L4.Skipped, 0, false)
	fmt.Printf("L4 (Cores):        %s  | similaridade: %s", l4Label, pct(res.L4.Similarity))
	if verbose {
		fmt.Printf("  | %s", metricsLine(res.L4.Metrics))
	}
	fmt.Println()
	fmt.Println()

	fastOk := res.L1.Passed && res.L4.Similarity >= ThresholdL4Fast && res.L2.Skipped && res.Authentic
	fullOk := !res.L2.Skipped && res.L2.Passed && res.L4.Similarity >= ThresholdL4Full

	fastStr, fullStr := "FALHOU", "FALHOU"
	if fastOk {
		fastStr = "PASSOU"
	}
	if fullOk {
		fullStr = "PASSOU"
	}
	fmt.Printf("Caminho rápido   (L1 + L4):      %s\n", fastStr)
	fmt.Printf("Caminho completo (L2 + L4):     %s\n", fullStr)
	fmt.Println()

	resultLabel := "ADULTERADA"
	if res.Authentic {
		resultLabel = "AUTÊNTICA"
	}
	fmt.Printf("→ RESULTADO: %s\n", resultLabel)
	fmt.Println("========================================")
	fmt.Println()
}

func metricsLine(m metrics.StageMetrics) string {
	return fmt.Sprintf("%8.1fms  heap:%+6.1fMB  rss:%7.1fMB  cpu:%.3fs",
		m.DurationMS, m.DeltaHeapMB, m.RSSMB, m.UserCPUS+m.SysCPUS)
}

func printSummary(results []Result) {
	fmt.Println("RESUMO")
	fmt.Printf("%-22s | %-6s | %-8s | %-8s | %-8s | %-9s | %s\n",
		"imagem", "rot°", "L1", "L2(min)", "L4", "caminho", "resultado")
	fmt.Println(strings.Repeat("-", 100))
	for _, res := range results {
		imgName := filepath.Base(res.ImagePath)
		if res.Error != "" {
			fmt.Printf("%-22s | ERRO\n", imgName)
			continue
		}
		l1 := pct(res.L1.Similarity)
		var l2s string
		if res.L2.Skipped {
			l2s = " PULADO "
		} else {
			l2s = pct(res.L2.Similarity)
		}
		var rotStr string
		if res.RotNetSkipped {
			rotStr = "  skip"
		} else {
			rotStr = fmt.Sprintf("%6d", res.RotNetDegrees)
		}
		fmt.Printf("%-22s | %6s | %-8s | %-8s | %-8s | %-9s | %s\n",
			imgName, rotStr, l1, l2s, pct(res.L4.Similarity), res.PathType,
			map[bool]string{true: "AUTÊNTICA", false: "ADULTERADA"}[res.Authentic])
	}
}

func main() {
	verbose := flag.Bool("verbose", false, "saída detalhada")
	metricsFlag := flag.Bool("metrics", false, "relatório JSON/texto de desempenho")
	refPath := flag.String("ref", "testdata/aletheia.jpg", "imagem de referência")
	testDir := flag.String("testdir", "testdata", "diretório de imagens candidatas")
	dinoModel := flag.String("dino", "models/dinov2_small.onnx", "ONNX DINOv2 (FP32 ou base para .int8)")
	rotnetModel := flag.String("rotnet", "models/rotnet_street_view.onnx", "ONNX RotNet")
	rotManifest := flag.String("rotnet-io", "models/rotnet_io.json", "JSON com nomes input/output do RotNet")
	rotIn := flag.String("rotnet-input", "", "força nome do input RotNet (opcional)")
	rotOut := flag.String("rotnet-output", "", "força nome do output RotNet (opcional)")
	preferInt8 := flag.Bool("int8", true, "usa versão .int8.onnx quando existir ao lado do .onnx")
	skipRotNet := flag.Bool("skip-rotnet", false, "desativa L0 RotNet (somente dev; requer export ONNX para produção)")
	dinoTiles := flag.String("dino-tiles", "1x1", "grelha DINO RxC ex. 2x2 (máx. 16 tiles)")
	dinoWorkers := flag.Int("dino-workers", 0, "sessões ONNX DINO paralelas (0=auto: 1 worker; >1 reparte tiles)")
	flag.Parse()

	if os.Getenv("PIPELINE_VERBOSE") == "1" {
		*verbose = true
	}
	if os.Getenv("PIPELINE_METRICS") == "1" {
		*metricsFlag = true
	}

	tRows, tCols, err := parseDinoTilesStr(*dinoTiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO: %v\n", err)
		os.Exit(1)
	}
	dw := *dinoWorkers
	if dw < 0 {
		fmt.Fprintf(os.Stderr, "ERRO: -dino-workers >= 0\n")
		os.Exit(1)
	}

	required := []string{*refPath, pickModelPath(*dinoModel, *preferInt8)}
	if !*skipRotNet {
		required = append(required, pickModelPath(*rotnetModel, *preferInt8))
	}
	for _, f := range required {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "ERRO: arquivo necessário não encontrado: %s\n", f)
			fmt.Fprintf(os.Stderr, "Exporte modelos com: python export_models.py\n")
			os.Exit(1)
		}
	}

	if *verbose || *metricsFlag {
		fmt.Println("Inicializando pipeline perceptual (DINOv2 728 + RotNet L0)...")
		fmt.Printf("  ORT_EP=%s (auto|cpu|cuda|rocm)\n", strings.TrimSpace(os.Getenv("ORT_EP")))
		fmt.Printf("  Referência: %s\n", *refPath)
		fmt.Printf("  DINOv2:     %s\n", pickModelPath(*dinoModel, *preferInt8))
		fmt.Printf("  DINO tiles: %dx%d (%d) | -dino-workers pedido: %d (0→1 sessão)\n", tRows, tCols, tRows*tCols, dw)
		if *skipRotNet {
			fmt.Println("  RotNet:     PULADO (--skip-rotnet)")
		} else {
			fmt.Printf("  RotNet:     %s\n", pickModelPath(*rotnetModel, *preferInt8))
		}
	}

	pipeline, err := NewPipeline(*dinoModel, *rotnetModel, *refPath, *rotManifest, *rotIn, *rotOut, *preferInt8, *skipRotNet, tRows, tCols, dw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO fatal: %v\n", err)
		os.Exit(1)
	}
	defer pipeline.Close()

	if *verbose {
		fmt.Printf("  DINO ONNX: %d shard(s), %d worker(s) efetivo(s)\n", len(pipeline.DinoShards), pipeline.DinoWorkers)
	}
	if *verbose && pipeline.Ref != nil && len(pipeline.Ref.DinoTiles) > 0 {
		fmt.Printf("  DINO: %d tile(s), CLS média tile0=%.6f\n", len(pipeline.Ref.DinoTiles), pipeline.Ref.DinoTiles[0].DinoCLSThreshold)
		fmt.Println()
	}

	entries, err := os.ReadDir(*testDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO ao ler testdir: %v\n", err)
		os.Exit(1)
	}
	extOK := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".tiff": true}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !extOK[ext] {
			continue
		}
		full := filepath.Join(*testDir, e.Name())
		absRef, _ := filepath.Abs(*refPath)
		absCand, _ := filepath.Abs(full)
		if absCand == absRef {
			continue
		}
		paths = append(paths, full)
	}
	sort.Strings(paths)

	var results []Result
	for _, imgPath := range paths {
		if *verbose {
			fmt.Printf("Avaliando: %s\n", imgPath)
		}
		res := evaluate(imgPath, pipeline)
		results = append(results, res)
		printResult(res, *verbose)
	}
	printSummary(results)
	if *metricsFlag {
		generateMetricsReport(results)
	}
}

func generateMetricsReport(results []Result) {
	perImage := make([]metrics.PerImageMetrics, 0, len(results))
	for _, res := range results {
		perImage = append(perImage, res.PerImageMetrics())
	}
	report := metrics.NewReport(perImage)
	outDir := os.Getenv("PIPELINE_METRICS_DIR")
	if outDir == "" {
		outDir = "."
	}
	_ = os.MkdirAll(outDir, 0755)
	jsonPath := filepath.Join(outDir, "metrics-report.json")
	jf, err := os.Create(jsonPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO metrics json: %v\n", err)
		return
	}
	_ = report.WriteJSON(jf)
	jf.Close()
	fmt.Printf("Relatório JSON: %s\n", jsonPath)
	txtPath := filepath.Join(outDir, "metrics-report.txt")
	tf, err := os.Create(txtPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO metrics txt: %v\n", err)
		return
	}
	report.WriteText(tf)
	tf.Close()
	fmt.Printf("Relatório texto: %s\n", txtPath)
	report.WriteText(os.Stdout)
}
