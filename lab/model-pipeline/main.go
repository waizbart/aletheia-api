package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/corona10/goimagehash"
	"github.com/disintegration/imaging"
	"github.com/lucasb-eyer/go-colorful"
	ort "github.com/yalue/onnxruntime_go"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	ThresholdL1Fast = 0.98
	ThresholdL4Fast = 0.97
	ThresholdL2Full = 0.97
	ThresholdL3Full = 0.97
	ThresholdL4Full = 0.97

	DinoInputSize       = 518  // múltiplo de 14 (ViT patch size)
	ConvNextInputSize   = 1088 // >=1080p, múltiplo de 32 (downsample total)

	ColorGridSize = 4  // grade 4×4
	ColorHistBins = 32 // bins por canal
)

// Dimensões derivadas dos modelos ONNX
const (
	dinoNumPatches = 37 * 37          // 1369  (518/14)^2
	dinoNumTokens  = dinoNumPatches + 1 // 1370, +1 CLS
	dinoHiddenDim  = 384

	// ConvNeXt sem GAP — mapa espacial
	ConvNextGridSize = 6     // grade espacial 6×6
	ConvNextChannels = 1024  // canais do feature map
	ConvNextSpatial  = ConvNextInputSize / 32 // 1088/32 = 34
	convSpatialBits  = ConvNextGridSize * ConvNextGridSize * ConvNextChannels // 36864

	colorTotalBins = ColorGridSize * ColorGridSize * 6 * ColorHistBins // 3072
)

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

// LayerResult armazena o resultado de uma camada da pipeline.
type LayerResult struct {
	Similarity float64
	Passed     bool
	Skipped    bool
	Hamming    int // apenas para L1 (pHash)
}

// ImageHashes contém todos os hashes de uma imagem.
type ImageHashes struct {
	PHash    *goimagehash.ImageHash
	DinoHash []byte // 384 bits
	ConvHash []byte // grid² × 1024 = 36864 bits → 4608 bytes
	ColorHash []byte // 3072 bits → 384 bytes

	// Thresholds de binarização fixos da imagem original.
	DinoThreshold    float32
	ConvThresholds   []float32 // 1024 thresholds, um por canal do ConvNeXt
	ColorThresholds  [6]float32
}

// Result agrega o resultado completo para uma imagem candidata.
type Result struct {
	ImagePath string
	L1        LayerResult
	L2        LayerResult
	L3        LayerResult
	L4        LayerResult
	PathType  string // "rápido" ou "completo"
	Authentic bool
	Error     string
}

// Pipeline contém as sessões ONNX e o hash de referência.
type Pipeline struct {
	DinoSession *ort.AdvancedSession
	ConvSession *ort.AdvancedSession
	DinoInput   *ort.Tensor[float32]
	DinoOutput  *ort.Tensor[float32]
	ConvInput   *ort.Tensor[float32]
	ConvOutput  *ort.Tensor[float32]
	Ref         *ImageHashes
}

// ---------------------------------------------------------------------------
// Utilitários
// ---------------------------------------------------------------------------

// hammingBits calcula a distância de Hamming entre dois slices de bytes.
// Usa bits.OnesCount8 (popcount acelerado por CPU) da stdlib.
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

// similarityFromHamming retorna 1 - (distância / totalBits).
func similarityFromHamming(dist, totalBits int) float64 {
	if totalBits == 0 {
		return 0
	}
	return 1.0 - float64(dist)/float64(totalBits)
}

// loadImage carrega uma imagem de arquivo usando a biblioteca imaging.
func loadImage(path string) (image.Image, error) {
	img, err := imaging.Open(path)
	if err != nil {
		return nil, fmt.Errorf("falha ao abrir imagem %s: %w", path, err)
	}
	return img, nil
}

// imageToRGBA converte image.Image para *image.NRGBA de forma consistente.
func imageToRGBA(img image.Image) *image.NRGBA {
	switch v := img.(type) {
	case *image.NRGBA:
		return v
	default:
		return imaging.Clone(img)
	}
}

// ---------------------------------------------------------------------------
// L1 — pHash (64 bits)
// ---------------------------------------------------------------------------

// computePHash calcula o perception hash de 64 bits de uma imagem.
func computePHash(img image.Image) (*goimagehash.ImageHash, error) {
	return goimagehash.PerceptionHash(img)
}

// phashSimilarity compara dois hashes de 64 bits e retorna similaridade [0,1].
func phashSimilarity(ref, cand *goimagehash.ImageHash) (float64, int, error) {
	dist, err := ref.Distance(cand)
	if err != nil {
		return 0, 0, err
	}
	sim := similarityFromHamming(dist, 64)
	return sim, dist, nil
}

// ---------------------------------------------------------------------------
// L2 — DINOv2 ViT-S/14 via ONNX
// ---------------------------------------------------------------------------

// preprocessDino prepara o tensor NCHW [1,3,518,518] para o DINOv2.
func preprocessDino(img image.Image) []float32 {
	resized := imaging.Resize(img, DinoInputSize, DinoInputSize, imaging.Lanczos)
	tensor := make([]float32, 1*3*DinoInputSize*DinoInputSize)

	mean := [3]float32{0.485, 0.456, 0.406}
	std := [3]float32{0.229, 0.224, 0.225}

	bounds := resized.Bounds()
	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := resized.At(x, y).RGBA()
			// RGBA() retorna valores 0-65535, converter para 0-1
			fr := float32(r) / 65535.0
			fg := float32(g) / 65535.0
			fb := float32(b) / 65535.0

			// NCHW: canal R
			tensor[idx] = (fr - mean[0]) / std[0]
			// canal G
			tensor[idx+1*DinoInputSize*DinoInputSize] = (fg - mean[1]) / std[1]
			// canal B
			tensor[idx+2*DinoInputSize*DinoInputSize] = (fb - mean[2]) / std[2]
			idx++
		}
	}
	return tensor
}

// extractDinoVector roda inferência e extrai o token CLS (primeiro token).
// Retorna um slice de 384 float32.
func extractDinoVector(p *Pipeline) ([]float32, error) {
	if err := p.DinoSession.Run(); err != nil {
		return nil, fmt.Errorf("falha na inferência DINOv2: %w", err)
	}

	// Output shape: [1, 1370, 384]
	// Token CLS está no índice 0: [batch=0][token=0][:]
	data := p.DinoOutput.GetData()
	cls := make([]float32, dinoHiddenDim)
	copy(cls, data[:dinoHiddenDim])
	return cls, nil
}

// binarizeVector binariza um vetor float32 com base no threshold.
// bit[i] = 1 se v[i] >= threshold, senão 0.
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

// computeThreshold calcula o threshold de binarização como a média do vetor.
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

// ---------------------------------------------------------------------------
// L3 — ConvNeXt V2-Base via ONNX
// ---------------------------------------------------------------------------

// preprocessConvNext prepara o tensor NCHW [1,3,H,W] para o ConvNeXt
// com resolução fixa de 1088×1088 (features locais em alta resolução).
func preprocessConvNext(img image.Image) []float32 {
	resized := imaging.Resize(img, ConvNextInputSize, ConvNextInputSize, imaging.Lanczos)
	tensor := make([]float32, 1*3*ConvNextInputSize*ConvNextInputSize)

	mean := [3]float32{0.485, 0.456, 0.406}
	std := [3]float32{0.229, 0.224, 0.225}

	rb := resized.Bounds()
	idx := 0
	for y := rb.Min.Y; y < rb.Max.Y; y++ {
		for x := rb.Min.X; x < rb.Max.X; x++ {
			r, g, b, _ := resized.At(x, y).RGBA()
			fr := float32(r) / 65535.0
			fg := float32(g) / 65535.0
			fb := float32(b) / 65535.0

			tensor[idx] = (fr - mean[0]) / std[0]
			tensor[idx+1*ConvNextInputSize*ConvNextInputSize] = (fg - mean[1]) / std[1]
			tensor[idx+2*ConvNextInputSize*ConvNextInputSize] = (fb - mean[2]) / std[2]
			idx++
		}
	}
	return tensor
}

// extractConvSpatial roda inferencia e retorna o mapa espacial [C, H, W].
func extractConvSpatial(p *Pipeline) ([]float32, error) {
	if err := p.ConvSession.Run(); err != nil {
		return nil, fmt.Errorf("falha na inferencia ConvNeXt: %w", err)
	}
	total := ConvNextChannels * ConvNextSpatial * ConvNextSpatial
	data := p.ConvOutput.GetData()
	feat := make([]float32, total)
	copy(feat, data[:total])
	return feat, nil
}

// spatialGridMean extrai features por celula da grade 6x6.
func spatialGridMean(featureMap []float32, spatialSize, gridSize, channels int) []float32 {
	result := make([]float32, 0, gridSize*gridSize*channels)
	for gy := 0; gy < gridSize; gy++ {
		y0 := gy * spatialSize / gridSize
		y1 := (gy + 1) * spatialSize / gridSize
		for gx := 0; gx < gridSize; gx++ {
			x0 := gx * spatialSize / gridSize
			x1 := (gx + 1) * spatialSize / gridSize
			for c := 0; c < channels; c++ {
				var sum float32
				count := 0
				for y := y0; y < y1; y++ {
					for x := x0; x < x1; x++ {
						idx := c*spatialSize*spatialSize + y*spatialSize + x
						sum += featureMap[idx]
						count++
					}
				}
				if count > 0 {
					result = append(result, sum/float32(count))
				} else {
					result = append(result, 0)
				}
			}
		}
	}
	return result
}

// computeConvThresholds calcula thresholds por canal da grade.
func computeConvThresholds(gridFeatures []float32) []float32 {
	channels := ConvNextChannels
	gridCells := ConvNextGridSize * ConvNextGridSize
	thresholds := make([]float32, channels)
	for c := 0; c < channels; c++ {
		var sum float64
		for g := 0; g < gridCells; g++ {
			idx := g*channels + c
			sum += float64(gridFeatures[idx])
		}
		thresholds[c] = float32(sum / float64(gridCells))
	}
	return thresholds
}

// binarizeSpatial binariza grid features com thresholds por canal.
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
// L4 — Hash de cores (via go-colorful)
// ---------------------------------------------------------------------------

// computeColorHash calcula o hash de cores completo:
// grade 4×4, cada região → 6 histogramas (H,S,V,L,a,b) de 32 bins.
// Retorna slice float32 de 3072 valores (antes da binarização).
func computeColorHash(img image.Image) []float32 {
	nrgba := imageToRGBA(img)
	bounds := nrgba.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Redimensionar para múltiplo de 4 mais próximo
	targetW := (w / 4) * 4
	targetH := (h / 4) * 4
	if targetW == 0 {
		targetW = 4
	}
	if targetH == 0 {
		targetH = 4
	}

	resized := imaging.Resize(nrgba, targetW, targetH, imaging.Lanczos)
	rb := resized.Bounds()
	rw := rb.Dx()
	rh := rb.Dy()

	cellW := rw / ColorGridSize
	cellH := rh / ColorGridSize

	// 16 regiões × 6 canais × 32 bins = 3072
	allFeatures := make([]float32, 0, colorTotalBins)

	for gy := 0; gy < ColorGridSize; gy++ {
		for gx := 0; gx < ColorGridSize; gx++ {
			x0 := rb.Min.X + gx*cellW
			y0 := rb.Min.Y + gy*cellH
			x1 := x0 + cellW
			y1 := y0 + cellH

			// Histogramas para H, S, V, L, a, b — cada um com ColorHistBins bins
			hHist := make([]float64, ColorHistBins)
			sHist := make([]float64, ColorHistBins)
			vHist := make([]float64, ColorHistBins)
			lHist := make([]float64, ColorHistBins)
			aHist := make([]float64, ColorHistBins)
			bHist := make([]float64, ColorHistBins)

			var totalPixels float64

			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					// Obter pixel RGBA (0-65535) e converter para uint8
					pr, pg, pb, _ := resized.At(x, y).RGBA()
					rgba := color.RGBA{
						R: uint8(pr >> 8),
						G: uint8(pg >> 8),
						B: uint8(pb >> 8),
						A: 255,
					}
					c, _ := colorful.MakeColor(rgba)

					// HSV via go-colorful (h: 0-360, s/v: 0-1)
					hVal, sVal, vVal := c.Hsv()
					// CIE Lab D65 via go-colorful (L: 0-100, a/b: ~-128 a 127)
					lVal, aVal, bVal := c.Lab()

					// H: 0-360 → bin 0-31
					hBin := int(hVal / 360.0 * float64(ColorHistBins))
					if hBin >= ColorHistBins {
						hBin = ColorHistBins - 1
					}
					hHist[hBin]++

					// S: 0-1 → bin 0-31
					sBin := int(sVal * float64(ColorHistBins))
					if sBin >= ColorHistBins {
						sBin = ColorHistBins - 1
					}
					sHist[sBin]++

					// V: 0-1 → bin 0-31
					vBin := int(vVal * float64(ColorHistBins))
					if vBin >= ColorHistBins {
						vBin = ColorHistBins - 1
					}
					vHist[vBin]++

					// L: 0-100 → bin 0-31
					lBin := int(lVal / 100.0 * float64(ColorHistBins))
					if lBin >= ColorHistBins {
						lBin = ColorHistBins - 1
					}
					if lBin < 0 {
						lBin = 0
					}
					lHist[lBin]++

					// a: ~-128 a 127 → deslocar para 0-255
					aNorm := (aVal + 128.0) / 255.0
					aBin := int(aNorm * float64(ColorHistBins))
					if aBin >= ColorHistBins {
						aBin = ColorHistBins - 1
					}
					if aBin < 0 {
						aBin = 0
					}
					aHist[aBin]++

					// b: ~-128 a 127 → deslocar para 0-255
					bNorm := (bVal + 128.0) / 255.0
					bBin := int(bNorm * float64(ColorHistBins))
					if bBin >= ColorHistBins {
						bBin = ColorHistBins - 1
					}
					if bBin < 0 {
						bBin = 0
					}
					bHist[bBin]++

					totalPixels++
				}
			}

			// Normalizar histogramas
			if totalPixels > 0 {
				normalizeHist(hHist, totalPixels)
				normalizeHist(sHist, totalPixels)
				normalizeHist(vHist, totalPixels)
				normalizeHist(lHist, totalPixels)
				normalizeHist(aHist, totalPixels)
				normalizeHist(bHist, totalPixels)
			}

			// Concatenar: 6 canais × 32 bins = 192 floats por região
			for _, val := range hHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range sHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range vHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range lHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range aHist {
				allFeatures = append(allFeatures, float32(val))
			}
			for _, val := range bHist {
				allFeatures = append(allFeatures, float32(val))
			}
		}
	}

	return allFeatures
}

// normalizeHist normaliza um histograma dividindo cada bin pelo total.
func normalizeHist(hist []float64, total float64) {
	if total == 0 {
		return
	}
	for i := range hist {
		hist[i] /= total
	}
}

// computeColorThresholds computa thresholds por canal (H,S,V,L,a,b)
// a partir do vetor de features flat (3072 valores).
// Cada canal tem 16 regiões × 32 bins = 512 valores.
func computeColorThresholds(features []float32) [6]float32 {
	var thresholds [6]float32
	for ch := 0; ch < 6; ch++ {
		var sum float64
		for region := 0; region < ColorGridSize*ColorGridSize; region++ {
			baseIdx := region*192 + ch*ColorHistBins
			for bin := 0; bin < ColorHistBins; bin++ {
				sum += float64(features[baseIdx+bin])
			}
		}
		thresholds[ch] = float32(sum / 512.0)
	}
	return thresholds
}

// binarizeColorVector binariza o vetor de features de cor usando
// thresholds específicos por canal (H,S,V,L,a,b).
func binarizeColorVector(features []float32, thresholds [6]float32) []byte {
	numBits := len(features)
	numBytes := (numBits + 7) / 8
	result := make([]byte, numBytes)

	for region := 0; region < ColorGridSize*ColorGridSize; region++ {
		for ch := 0; ch < 6; ch++ {
			baseIdx := region*192 + ch*ColorHistBins
			for bin := 0; bin < ColorHistBins; bin++ {
				i := baseIdx + bin
				if features[i] >= thresholds[ch] {
					byteIdx := i / 8
					bitIdx := uint(i % 8)
					result[byteIdx] |= 1 << bitIdx
				}
			}
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Pipeline — inicialização
// ---------------------------------------------------------------------------

// initOnnxRuntime inicializa o runtime ONNX.
func initOnnxRuntime() error {
	// A onnxruntime_go procura por "onnxruntime.so", mas o arquivo real
	// se chama "libonnxruntime.so". Setamos explicitamente o caminho.
	libPath := os.Getenv("ONNX_RUNTIME_LIB")
	if libPath == "" {
		// Tentar caminhos comuns
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

// openOnnxSessionAdvanced abre uma sessão ONNX com shapes fixos.
func openOnnxSessionAdvanced(
	modelPath string,
	inputNames, outputNames []string,
	inputShape, outputShape ort.Shape,
) (*ort.AdvancedSession, *ort.Tensor[float32], *ort.Tensor[float32], error) {

	inputData := make([]float32, inputShape.FlattenedSize())

	inputTensor, err := ort.NewTensor(inputShape, inputData)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("falha ao criar input tensor: %w", err)
	}

	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("falha ao criar output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(
		modelPath,
		inputNames,
		outputNames,
		[]ort.Value{inputTensor},
		[]ort.Value{outputTensor},
		nil,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("falha ao criar sessão ONNX: %w", err)
	}

	return session, inputTensor, outputTensor, nil
}

// closeOnnxSession libera os recursos de uma sessão avançada.
func closeOnnxSession(session *ort.AdvancedSession) {
	if session != nil {
		session.Destroy()
	}
}

// computeReferenceHashes processa a imagem de referência e calcula todos os hashes.
func computeReferenceHashes(refImg image.Image, p *Pipeline) (*ImageHashes, error) {
	ref := &ImageHashes{}

	// L1 — pHash
	phash, err := computePHash(refImg)
	if err != nil {
		return nil, fmt.Errorf("falha ao calcular pHash de referência: %w", err)
	}
	ref.PHash = phash

	// L2 — DINOv2
	dinoPre := preprocessDino(refImg)
	copy(p.DinoInput.GetData(), dinoPre)
	dinoVec, err := extractDinoVector(p)
	if err != nil {
		return nil, fmt.Errorf("falha ao extrair vetor DINOv2 de referência: %w", err)
	}
	ref.DinoThreshold = computeThreshold(dinoVec)
	ref.DinoHash = binarizeVector(dinoVec, ref.DinoThreshold)

	// L3 — ConvNeXt
	convPre := preprocessConvNext(refImg)
	copy(p.ConvInput.GetData(), convPre)
	convMap, err := extractConvSpatial(p)
	if err != nil {
		return nil, fmt.Errorf("falha ao extrair mapa ConvNeXt de referencia: %w", err)
	}
	gridFeat := spatialGridMean(convMap, ConvNextSpatial, ConvNextGridSize, ConvNextChannels)
	ref.ConvThresholds = computeConvThresholds(gridFeat)
	ref.ConvHash = binarizeSpatial(gridFeat, ref.ConvThresholds)

	// L4 — Cores (thresholds por canal)
	colorVec := computeColorHash(refImg)
	ref.ColorThresholds = computeColorThresholds(colorVec)
	ref.ColorHash = binarizeColorVector(colorVec, ref.ColorThresholds)

	return ref, nil
}

// NewPipeline inicializa a pipeline: ONNX Runtime, sessões e referência.
func NewPipeline(dinoModel, convModel, refPath string) (*Pipeline, error) {
	if err := initOnnxRuntime(); err != nil {
		return nil, err
	}

	// Abrir sessão DINOv2
	dinoShape := ort.NewShape(1, 3, DinoInputSize, DinoInputSize)
	dinoOutShape := ort.NewShape(1, dinoNumTokens, dinoHiddenDim)
	dinoSess, dinoIn, dinoOut, err := openOnnxSessionAdvanced(
		dinoModel,
		[]string{"pixel_values"},
		[]string{"last_hidden_state"},
		dinoShape, dinoOutShape,
	)
	if err != nil {
		return nil, fmt.Errorf("DINOv2: %w", err)
	}

	// Abrir sessão ConvNeXt
	convShape := ort.NewShape(1, 3, ConvNextInputSize, ConvNextInputSize)
	convOutShape := ort.NewShape(1, ConvNextChannels, ConvNextSpatial, ConvNextSpatial)
	convSess, convIn, convOut, err := openOnnxSessionAdvanced(
		convModel,
		[]string{"input"},
		[]string{"features"},
		convShape, convOutShape,
	)
	if err != nil {
		closeOnnxSession(dinoSess)
		return nil, fmt.Errorf("ConvNeXt: %w", err)
	}

	p := &Pipeline{
		DinoSession: dinoSess,
		ConvSession: convSess,
		DinoInput:   dinoIn,
		DinoOutput:  dinoOut,
		ConvInput:   convIn,
		ConvOutput:  convOut,
	}

	// Carregar referência
	refImg, err := loadImage(refPath)
	if err != nil {
		closeOnnxSession(dinoSess)
		closeOnnxSession(convSess)
		return nil, fmt.Errorf("referência: %w", err)
	}

	ref, err := computeReferenceHashes(refImg, p)
	if err != nil {
		closeOnnxSession(dinoSess)
		closeOnnxSession(convSess)
		return nil, fmt.Errorf("hashes de referência: %w", err)
	}
	p.Ref = ref

	return p, nil
}

// Close libera os recursos da pipeline.
func (p *Pipeline) Close() {
	if p.DinoSession != nil {
		p.DinoSession.Destroy()
	}
	if p.ConvSession != nil {
		p.ConvSession.Destroy()
	}
	ort.DestroyEnvironment()
}

// ---------------------------------------------------------------------------
// Avaliação individual
// ---------------------------------------------------------------------------

// evaluatePHash calcula similaridade L1 (pHash).
func evaluatePHash(ref *ImageHashes, img image.Image) LayerResult {
	candPHash, err := computePHash(img)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}
	}
	sim, dist, err := phashSimilarity(ref.PHash, candPHash)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}
	}
	passed := sim >= ThresholdL1Fast
	return LayerResult{
		Similarity: sim,
		Passed:     passed,
		Hamming:    dist,
	}
}

// evaluateDino calcula similaridade L2 (DINOv2).
func evaluateDino(ref *ImageHashes, img image.Image, p *Pipeline) LayerResult {
	pre := preprocessDino(img)
	copy(p.DinoInput.GetData(), pre)

	dinoVec, err := extractDinoVector(p)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false, Skipped: false}
	}

	candHash := binarizeVector(dinoVec, ref.DinoThreshold)
	dist := hammingBits(ref.DinoHash, candHash)
	if dist < 0 {
		return LayerResult{Similarity: 0, Passed: false}
	}
	sim := similarityFromHamming(dist, dinoHiddenDim)
	passed := sim >= ThresholdL2Full
	return LayerResult{
		Similarity: sim,
		Passed:     passed,
	}
}

// evaluateConvNext calcula similaridade L3 (ConvNeXt).
func evaluateConvNext(ref *ImageHashes, img image.Image, p *Pipeline) LayerResult {
	pre := preprocessConvNext(img)
	copy(p.ConvInput.GetData(), pre)

	convMap, err := extractConvSpatial(p)
	if err != nil {
		return LayerResult{Similarity: 0, Passed: false}
	}

	gridFeat := spatialGridMean(convMap, ConvNextSpatial, ConvNextGridSize, ConvNextChannels)
	candHash := binarizeSpatial(gridFeat, ref.ConvThresholds)
	dist := hammingBits(ref.ConvHash, candHash)
	if dist < 0 {
		return LayerResult{Similarity: 0, Passed: false}
	}
	sim := similarityFromHamming(dist, convSpatialBits)
	passed := sim >= ThresholdL3Full
	return LayerResult{
		Similarity: sim,
		Passed:     passed,
	}
}

// evaluateColor calcula similaridade L4 (hash de cores com thresholds por canal).
func evaluateColor(ref *ImageHashes, img image.Image) LayerResult {
	colorVec := computeColorHash(img)
	candHash := binarizeColorVector(colorVec, ref.ColorThresholds)
	dist := hammingBits(ref.ColorHash, candHash)
	if dist < 0 {
		return LayerResult{Similarity: 0, Passed: false}
	}
	sim := similarityFromHamming(dist, colorTotalBins)
	passed := sim >= ThresholdL4Full
	return LayerResult{
		Similarity: sim,
		Passed:     passed,
	}
}

// evaluate executa a pipeline perceptual completa para uma imagem candidata.
func evaluate(candidatePath string, p *Pipeline) Result {
	res := Result{ImagePath: candidatePath}

	img, err := loadImage(candidatePath)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	// L1 — pHash
	res.L1 = evaluatePHash(p.Ref, img)

	// Decisão: caminho rápido ou completo
	if res.L1.Passed {
		// Caminho rápido: pula L2 e L3, vai direto para L4
		res.L2 = LayerResult{Similarity: 0, Passed: false, Skipped: true}
		res.L3 = LayerResult{Similarity: 0, Passed: false, Skipped: true}
		res.L4 = evaluateColor(p.Ref, img)

		if res.L4.Similarity >= ThresholdL4Fast {
			res.PathType = "rápido"
			res.Authentic = true
		} else {
			// L1 passou mas L4 falhou → cai no caminho completo
			// (precisa computar L2 e L3)
			res.L2 = evaluateDino(p.Ref, img, p)
			res.L3 = evaluateConvNext(p.Ref, img, p)

			if !res.L2.Skipped && !res.L3.Skipped &&
				res.L2.Similarity >= ThresholdL2Full &&
				res.L3.Similarity >= ThresholdL3Full &&
				res.L4.Similarity >= ThresholdL4Full {
				res.PathType = "completo"
				res.Authentic = true
			} else {
				res.PathType = "completo"
				res.Authentic = false
			}
		}
	} else {
		// Caminho completo
		res.L2 = evaluateDino(p.Ref, img, p)
		res.L3 = evaluateConvNext(p.Ref, img, p)
		res.L4 = evaluateColor(p.Ref, img)

		if !res.L2.Skipped && !res.L3.Skipped &&
			res.L2.Similarity >= ThresholdL2Full &&
			res.L3.Similarity >= ThresholdL3Full &&
			res.L4.Similarity >= ThresholdL4Full {
			res.PathType = "completo"
			res.Authentic = true
		} else {
			res.PathType = "completo"
			res.Authentic = false
		}
	}

	return res
}

// ---------------------------------------------------------------------------
// Formatação de saída
// ---------------------------------------------------------------------------

// layerLabel retorna o rótulo textual de uma camada.
func layerLabel(sim float64, passed, skipped bool, hamming int, showHamming bool) string {
	if skipped {
		return "PULADO"
	}
	if passed {
		return "PASSOU"
	}
	return "FALHOU"
}

// pct formata um float64 como percentual com 2 casas decimais.
func pct(v float64) string {
	return fmt.Sprintf("%6.2f%%", v*100)
}

// printResult imprime o resultado detalhado de uma imagem.
func printResult(res Result) {
	fmt.Println("========================================")
	fmt.Printf("Imagem: %s\n", res.ImagePath)
	fmt.Println("========================================")

	if res.Error != "" {
		fmt.Printf("ERRO: %s\n", res.Error)
		fmt.Println("========================================")
		fmt.Println()
		return
	}

	// L1
	l1Label := layerLabel(res.L1.Similarity, res.L1.Passed, res.L1.Skipped, res.L1.Hamming, true)
	l1Str := fmt.Sprintf("L1 (pHash):        %s  | similaridade: %s | hamming: %d",
		l1Label, pct(res.L1.Similarity), res.L1.Hamming)
	fmt.Println(l1Str)

	// L2
	if !res.L2.Skipped {
		l2Label := layerLabel(res.L2.Similarity, res.L2.Passed, res.L2.Skipped, 0, false)
		fmt.Printf("L2 (DINOv2-S):     %s  | similaridade: %s\n",
			l2Label, pct(res.L2.Similarity))
	} else {
		fmt.Printf("L2 (DINOv2-S):     PULADO\n")
	}

	// L3
	if !res.L3.Skipped {
		l3Label := layerLabel(res.L3.Similarity, res.L3.Passed, res.L3.Skipped, 0, false)
		fmt.Printf("L3 (ConvNeXt V2):  %s  | similaridade: %s\n",
			l3Label, pct(res.L3.Similarity))
	} else {
		fmt.Printf("L3 (ConvNeXt V2):  PULADO\n")
	}

	// L4
	l4Label := layerLabel(res.L4.Similarity, res.L4.Passed, res.L4.Skipped, 0, false)
	fmt.Printf("L4 (Cores):        %s  | similaridade: %s\n",
		l4Label, pct(res.L4.Similarity))

	fmt.Println()

	// Caminhos
	fastOk := res.L1.Passed && res.L4.Similarity >= ThresholdL4Fast
	fullOk := !res.L2.Skipped && !res.L3.Skipped &&
		res.L2.Similarity >= ThresholdL2Full &&
		res.L3.Similarity >= ThresholdL3Full &&
		res.L4.Similarity >= ThresholdL4Full

	fastStr := "FALHOU"
	if fastOk {
		fastStr = "PASSOU"
	}
	fullStr := "FALHOU"
	if fullOk {
		fullStr = "PASSOU"
	}

	fmt.Printf("Caminho rápido   (L1 + L4):      %s\n", fastStr)
	fmt.Printf("Caminho completo (L2 + L3 + L4): %s\n", fullStr)
	fmt.Println()

	resultLabel := "ADULTERADA"
	if res.Authentic {
		resultLabel = "AUTÊNTICA"
	}
	fmt.Printf("→ RESULTADO: %s\n", resultLabel)
	fmt.Println("========================================")
	fmt.Println()
}

// printSummary imprime a tabela resumo ao final.
func printSummary(results []Result) {
	fmt.Println("RESUMO")
	fmt.Printf("%-22s | %-8s | %-8s | %-8s | %-8s | %-9s | %s\n",
		"imagem", "L1", "L2", "L3", "L4", "caminho", "resultado")
	fmt.Println(strings.Repeat("-", 100))

	for _, res := range results {
		imgName := filepath.Base(res.ImagePath)

		var l1Str, l2Str, l3Str, l4Str, pathStr, resultStr string

		if res.Error != "" {
			l1Str = "  ERRO  "
			l2Str = ""
			l3Str = ""
			l4Str = ""
			pathStr = ""
			resultStr = "ERRO"
		} else {
			if res.L1.Skipped {
				l1Str = " PULADO "
			} else {
				l1Str = pct(res.L1.Similarity)
			}
			if res.L2.Skipped {
				l2Str = " PULADO "
			} else {
				l2Str = pct(res.L2.Similarity)
			}
			if res.L3.Skipped {
				l3Str = " PULADO "
			} else {
				l3Str = pct(res.L3.Similarity)
			}
			l4Str = pct(res.L4.Similarity)
			pathStr = res.PathType
			if res.Authentic {
				resultStr = "AUTÊNTICA"
			} else {
				resultStr = "ADULTERADA"
			}
		}

		fmt.Printf("%-22s | %-8s | %-8s | %-8s | %-8s | %-9s | %s\n",
			imgName, l1Str, l2Str, l3Str, l4Str, pathStr, resultStr)
	}
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	verbose := flag.Bool("verbose", false, "saída detalhada")
	refPath := flag.String("ref", "testdata/aletheia.jpg", "caminho da imagem de referência")
	testDir := flag.String("testdir", "testdata", "diretório com imagens de teste")
	dinoModel := flag.String("dino", "models/dinov2_small.onnx", "modelo ONNX DINOv2")
	convModel := flag.String("convnext", "models/convnextv2_base.onnx", "modelo ONNX ConvNeXt")
	flag.Parse()

	// Se a variável de ambiente PIPELINE_VERBOSE estiver definida, sobrescreve o flag
	if os.Getenv("PIPELINE_VERBOSE") == "1" {
		*verbose = true
	}

	// Verificar arquivos obrigatórios
	requiredFiles := []string{*refPath, *dinoModel, *convModel}
	for _, f := range requiredFiles {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "ERRO: arquivo necessário não encontrado: %s\n", f)
			fmt.Fprintf(os.Stderr, "Certifique-se de que os modelos ONNX foram exportados com export_models.py\n")
			os.Exit(1)
		}
	}

	if *verbose {
		fmt.Println("Inicializando pipeline perceptual...")
		fmt.Printf("  Referência: %s\n", *refPath)
		fmt.Printf("  DINOv2:     %s\n", *dinoModel)
		fmt.Printf("  ConvNeXt:   %s\n", *convModel)
	}

	// Inicializar pipeline
	pipeline, err := NewPipeline(*dinoModel, *convModel, *refPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO fatal ao inicializar pipeline: %v\n", err)
		os.Exit(1)
	}
	defer pipeline.Close()

	if *verbose {
		fmt.Printf("Thresholds de binarização:\n")
		fmt.Printf("  DINOv2:   %.6f\n", pipeline.Ref.DinoThreshold)
		ct3 := pipeline.Ref.ConvThresholds
		if len(ct3) > 0 {
			fmt.Printf("  ConvNeXt: %.6f (media de %d canais)\n", ct3[0], len(ct3))
		}
		ct := pipeline.Ref.ColorThresholds
		fmt.Printf("  Cores (H,S,V,L,a,b): %.6f, %.6f, %.6f, %.6f, %.6f, %.6f\n",
			ct[0], ct[1], ct[2], ct[3], ct[4], ct[5])
		fmt.Printf("  ConvNeXt input: %dx%d\n", ConvNextInputSize, ConvNextInputSize)
		fmt.Println()
	}

	// Listar imagens de teste
	entries, err := os.ReadDir(*testDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERRO ao ler diretório de teste %s: %v\n", *testDir, err)
		os.Exit(1)
	}

	// Extensões de imagem suportadas
	extensions := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".tiff": true,
	}

	var testImages []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !extensions[ext] {
			continue
		}
		// Ignorar a imagem de referência (já foi processada)
		fullPath := filepath.Join(*testDir, entry.Name())
		absRef, _ := filepath.Abs(*refPath)
		absCand, _ := filepath.Abs(fullPath)
		if absCand == absRef {
			continue
		}
		testImages = append(testImages, fullPath)
	}

	sort.Strings(testImages)

	if *verbose {
		fmt.Printf("Encontradas %d imagens de teste para avaliar.\n\n", len(testImages))
	}

	// Avaliar cada imagem
	var results []Result
	for _, imgPath := range testImages {
		if *verbose {
			fmt.Printf("Avaliando: %s\n", imgPath)
		}
		res := evaluate(imgPath, pipeline)
		results = append(results, res)
		printResult(res)
	}

	// Tabela resumo
	printSummary(results)
}
