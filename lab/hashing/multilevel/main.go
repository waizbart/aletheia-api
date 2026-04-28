package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"math/bits"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ORIGINAL_FILE_NAME = "aletheia.jpg"
	// L2: pHash settings (256-bit hash using 16x16 DCT block)
	PHASH_THRESHOLD = 10 // More lenient for 256 bits
	PHASH_DIM       = 64
	PHASH_BLOCK     = 16

	// L3: MobileViT settings
	L3_THRESHOLD     = 0.85 // Lower threshold for "Bag of Tiles" matching
	TILE_SIZE        = 224
	MODEL_INPUT_SIZE = 256
	LSH_BITS         = 64
	STANDARDIZED_DIM = 896 // 4 tiles of 224
)

type PipelineResult struct {
	Level   int
	Match   bool
	Details string
}

var onnxServerURL string

func main() {
	onnxServerURL = os.Getenv("ONNX_SERVER_URL")
	if onnxServerURL == "" {
		onnxServerURL = "http://localhost:8080/predict"
	}

	testdataDir := "/testdata"
	if _, err := os.Stat(testdataDir); os.IsNotExist(err) {
		testdataDir = "../../testdata"
	}
	originalPath := filepath.Join(testdataDir, ORIGINAL_FILE_NAME)

	fmt.Printf("Starting Multi-level Verification Pipeline\n")
	fmt.Printf("Original: %s\n\n", originalPath)

	// Wait for workers to be ready
	for i := 0; i < 30; i++ {
		resp, err := http.Get(strings.Replace(onnxServerURL, "/predict", "/", 1))
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(1 * time.Second)
	}

	originalFeatures, err := processImage(originalPath)
	if err != nil {
		log.Fatalf("Failed to process original image: %v", err)
	}

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		log.Fatalf("Failed to read testdata: %v", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	fmt.Printf("%-30s | %-10s | %-5s | %s\n", "Filename", "Result", "Lvl", "Details")
	fmt.Println(strings.Repeat("-", 80))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(testdataDir, entry.Name())
		res := runPipeline(path, originalFeatures)
		
		matchStr := "MISMATCH"
		if res.Match {
			matchStr = "MATCH"
		}
		
		filename := entry.Name()
		if filename == ORIGINAL_FILE_NAME {
			filename += " (*)"
		}
		fmt.Printf("%-30s | %-10s | L%d    | %s\n", filename, matchStr, res.Level, res.Details)
	}
}

type ImageFeatures struct {
	Path     string
	SHA256   [32]byte
	PHash    []uint64
	L3Hashes []uint64
}

func processImage(path string) (*ImageFeatures, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	h1 := sha256.Sum256(data)

	file, _ := os.Open(path)
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	stdImg := resizeImage(img, STANDARDIZED_DIM, STANDARDIZED_DIM)
	h2 := computePHashLarge(stdImg)
	h3, err := computeL3Hashes(stdImg)
	if err != nil {
		return nil, err
	}

	return &ImageFeatures{
		Path:     path,
		SHA256:   h1,
		PHash:    h2,
		L3Hashes: h3,
	}, nil
}

func runPipeline(path string, original *ImageFeatures) PipelineResult {
	data, err := os.ReadFile(path)
	if err == nil {
		h1 := sha256.Sum256(data)
		if h1 == original.SHA256 {
			return PipelineResult{Level: 1, Match: true, Details: "Binary Identical"}
		}
	}

	file, _ := os.Open(path)
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return PipelineResult{Level: 0, Match: false, Details: "Decode Error"}
	}

	// Standardization fixes cropping
	stdImg := resizeImage(img, STANDARDIZED_DIM, STANDARDIZED_DIM)

	h2 := computePHashLarge(stdImg)
	dist2 := hammingDistanceLarge(h2, original.PHash)
	if dist2 <= PHASH_THRESHOLD {
		return PipelineResult{Level: 2, Match: true, Details: fmt.Sprintf("pHash Dist: %d", dist2)}
	}

	// L3 with Rotation check + Bag of Tiles
	bestSim := 0.0
	bestAngle := 0
	
	angles := []int{0, 90, 180, 270}
	for _, angle := range angles {
		var currentImg image.Image
		if angle == 0 {
			currentImg = stdImg
		} else {
			currentImg = rotateImage(stdImg, angle)
		}

		h3, err := computeL3Hashes(currentImg)
		if err != nil {
			continue
		}

		sim := calculateSimilarityBag(h3, original.L3Hashes)
		if sim > bestSim {
			bestSim = sim
			bestAngle = angle
		}
		if bestSim >= L3_THRESHOLD {
			break
		}
	}

	if bestSim >= L3_THRESHOLD {
		details := fmt.Sprintf("Semantic Sim: %.2f%%", bestSim*100)
		if bestAngle != 0 {
			details += fmt.Sprintf(" (Rot: %d)", bestAngle)
		}
		return PipelineResult{Level: 3, Match: true, Details: details}
	}

	return PipelineResult{Level: 3, Match: false, Details: fmt.Sprintf("Sim: %.2f%% (Dist2: %d)", bestSim*100, dist2)}
}

// calculateSimilarityBag allows tiles to match anywhere (robust to shifting)
func calculateSimilarityBag(h1, h2 []uint64) float64 {
	if len(h1) == 0 || len(h2) == 0 {
		return 0
	}
	matchCount := 0
	m2 := make(map[uint64]int)
	for _, h := range h2 {
		m2[h]++
	}
	for _, h := range h1 {
		if m2[h] > 0 {
			matchCount++
			m2[h]--
		}
	}
	sim1 := float64(matchCount) / float64(len(h1))
	sim2 := float64(matchCount) / float64(len(h2))
	if sim1 < sim2 { return sim1 }
	return sim2
}

// --- L2: 256-bit pHash ---

func computePHashLarge(img image.Image) []uint64 {
	resized := resizeAndGrayscale(img, PHASH_DIM, PHASH_DIM)
	var pixels [PHASH_DIM][PHASH_DIM]float64
	for y := 0; y < PHASH_DIM; y++ {
		for x := 0; x < PHASH_DIM; x++ {
			pixels[y][x] = float64(resized.GrayAt(x, y).Y)
		}
	}
	var dct [PHASH_DIM][PHASH_DIM]float64
	dct2DLarge(&pixels, &dct)

	var values []float64
	var sum float64
	for y := 0; y < PHASH_BLOCK; y++ {
		for x := 0; x < PHASH_BLOCK; x++ {
			if x == 0 && y == 0 {
				continue
			}
			v := dct[y][x]
			values = append(values, v)
			sum += v
		}
	}
	median := sum / float64(len(values))

	hashes := make([]uint64, 4)
	for i, v := range values {
		if v > median {
			hashes[i/64] |= 1 << uint(i%64)
		}
	}
	return hashes
}

func dct2DLarge(input *[PHASH_DIM][PHASH_DIM]float64, output *[PHASH_DIM][PHASH_DIM]float64) {
	const N = PHASH_DIM
	for u := 0; u < N; u++ {
		for v := 0; v < N; v++ {
			var sum float64
			for x := 0; x < N; x++ {
				for y := 0; y < N; y++ {
					sum += input[x][y] * 
						math.Cos((float64(2*x+1)*float64(u)*math.Pi)/(2*N)) * 
						math.Cos((float64(2*y+1)*float64(v)*math.Pi)/(2*N))
				}
			}
			cu, cv := 1.0, 1.0
			if u == 0 { cu = 1.0 / math.Sqrt(2) }
			if v == 0 { cv = 1.0 / math.Sqrt(2) }
			output[u][v] = 0.25 * cu * cv * sum
		}
	}
}

func hammingDistanceLarge(a, b []uint64) int {
	dist := 0
	for i := 0; i < len(a); i++ {
		dist += bits.OnesCount64(a[i] ^ b[i])
	}
	return dist
}

// --- Image Utils ---

func resizeImage(img image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	srcBounds := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx := srcBounds.Min.X + (x*(srcBounds.Dx()))/w
			sy := srcBounds.Min.Y + (y*(srcBounds.Dy()))/h
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	return dst
}

func resizeAndGrayscale(img image.Image, w, h int) *image.Gray {
	dst := image.NewGray(image.Rect(0, 0, w, h))
	srcBounds := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx := srcBounds.Min.X + (x*(srcBounds.Dx()))/w
			sy := srcBounds.Min.Y + (y*(srcBounds.Dy()))/h
			c := img.At(sx, sy)
			dst.Set(x, y, c)
		}
	}
	return dst
}

func rotateImage(img image.Image, angle int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	var newW, newH int
	if angle == 90 || angle == 270 {
		newW, newH = h, w
	} else {
		newW, newH = w, h
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch angle {
			case 90:
				nx, ny = h-y-1, x
			case 180:
				nx, ny = w-x-1, h-y-1
			case 270:
				nx, ny = y, w-x-1
			default:
				nx, ny = x, y
			}
			dst.Set(nx, ny, img.At(x, y))
		}
	}
	return dst
}

// --- L3: MobileViT + Parallel Tiling + Remote LSH ---

var lshPlanes [][]float32
var once sync.Once

func initLSH() {
	once.Do(func() {
		rand.Seed(42)
		lshPlanes = make([][]float32, LSH_BITS)
		for i := 0; i < LSH_BITS; i++ {
			lshPlanes[i] = make([]float32, 1000)
			for j := 0; j < 1000; j++ {
				lshPlanes[i][j] = float32(rand.NormFloat64())
			}
		}
	})
}

func computeL3Hashes(img image.Image) ([]uint64, error) {
	initLSH()
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	var hashes []uint64
	var mu sync.Mutex
	var wg sync.WaitGroup

	for y := 0; y <= height-TILE_SIZE; y += TILE_SIZE {
		for x := 0; x <= width-TILE_SIZE; x += TILE_SIZE {
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				tile := cropImage(img, tx, ty, TILE_SIZE, TILE_SIZE)
				inputTensor, _ := preprocessForModel(tile)
				features, err := remoteInference(inputTensor)
				if err != nil { return }
				h := computeLSH(features)
				mu.Lock()
				hashes = append(hashes, h)
				mu.Unlock()
			}(x, y)
		}
	}

	wg.Wait()
	return hashes, nil
}

func cropImage(img image.Image, x, y, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, image.Point{x, y}, draw.Src)
	return dst
}

func preprocessForModel(img image.Image) ([]float32, error) {
	resized := image.NewRGBA(image.Rect(0, 0, MODEL_INPUT_SIZE, MODEL_INPUT_SIZE))
	srcBounds := img.Bounds()
	for y := 0; y < MODEL_INPUT_SIZE; y++ {
		for x := 0; x < MODEL_INPUT_SIZE; x++ {
			sx := srcBounds.Min.X + (x*(srcBounds.Dx()))/MODEL_INPUT_SIZE
			sy := srcBounds.Min.Y + (y*(srcBounds.Dy()))/MODEL_INPUT_SIZE
			resized.Set(x, y, img.At(sx, sy))
		}
	}

	data := make([]float32, 3*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE)
	for y := 0; y < MODEL_INPUT_SIZE; y++ {
		for x := 0; x < MODEL_INPUT_SIZE; x++ {
			r, g, b, _ := resized.At(x, y).RGBA()
			data[0*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE+y*MODEL_INPUT_SIZE+x] = float32(r) / 65535.0
			data[1*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE+y*MODEL_INPUT_SIZE+x] = float32(g) / 65535.0
			data[2*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE+y*MODEL_INPUT_SIZE+x] = float32(b) / 65535.0
		}
	}
	return data, nil
}

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

func remoteInference(inputData []float32) ([]float32, error) {
	buf := new(bytes.Buffer)
	for _, f := range inputData {
		binary.Write(buf, binary.LittleEndian, f)
	}

	resp, err := httpClient.Post(onnxServerURL, "application/octet-stream", buf)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	resBody, _ := io.ReadAll(resp.Body)
	output := make([]float32, len(resBody)/4)
	for i := 0; i < len(output); i++ {
		bits := binary.LittleEndian.Uint32(resBody[i*4 : (i+1)*4])
		output[i] = math.Float32frombits(bits)
	}

	return output, nil
}

func computeLSH(features []float32) uint64 {
	var hash uint64
	for i := 0; i < LSH_BITS; i++ {
		var dot float32
		for j := 0; j < len(features); j++ {
			dot += features[j] * lshPlanes[i][j]
		}
		if dot > 0 {
			hash |= 1 << uint(i)
		}
	}
	return hash
}
