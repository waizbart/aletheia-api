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

	"gocv.io/x/gocv"
)

const (
	ORIGINAL_FILE_NAME = "aletheia.jpg"

	// L3: MobileViT settings
	L3_THRESHOLD         = 0.93 // Min fraction of tiles that must match
	L3_HAMMING_THRESHOLD = 6    // Max bits difference per tile (total across all LSH_WORDS)
	TILE_SIZE            = 256
	MODEL_INPUT_SIZE     = 256
	LSH_BITS             = 256
	LSH_WORDS            = LSH_BITS / 64 // Number of uint64 words in the hash

	// Proportional resize cap: 3 megapixels (2048×1536)
	// Images smaller than this keep their original resolution.
	MAX_IMAGE_AREA = 3_145_728
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

		filename := entry.Name()
		if filename == ORIGINAL_FILE_NAME {
			filename += " (*)"
		}

		matchStr := "MISMATCH"
		if res.Match {
			matchStr = "MATCH"
		}
		fmt.Printf("%-30s | %-10s | L%d    | %s\n", filename, matchStr, res.Level, res.Details)
	}
}

// TileDict is a dictionary keyed by "col:row" position, mapping to the multi-word SimHash.
// Each hash is LSH_WORDS × uint64 (e.g. 4 words = 256 bits).
// This is the structure that gets stored on the blockchain.
type TileDict map[string][]uint64

// ImageFeatures holds all extracted features for an image.
type ImageFeatures struct {
	Path    string
	SHA256  [32]byte
	TileMap TileDict // SimHash per tile, keyed by "col:row"
}

func processImage(path string) (*ImageFeatures, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	// 1. Downscale to 3MP (proportional, aspect ratio preserved)
	stdImg := resizeProportional(img)

	// 2. SHA-256 of the normalized pixel data at 0° (for L1 exact match)
	h1 := sha256Image(stdImg)

	// 3. Compute L3 tile hashes (SimHash per tile at original 0° orientation)
	tileMap, err := computeL3TileDict(stdImg)
	if err != nil {
		return nil, err
	}

	return &ImageFeatures{
		Path:    path,
		SHA256:  h1,
		TileMap: tileMap,
	}, nil
}

func runPipeline(path string, original *ImageFeatures) PipelineResult {
	file, err := os.Open(path)
	if err != nil {
		return PipelineResult{Level: 0, Match: false, Details: "Open Error"}
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return PipelineResult{Level: 0, Match: false, Details: "Decode Error"}
	}

	// 1. Downscale to 3MP (proportional, aspect ratio preserved)
	stdImg := resizeProportional(img)

	// 2. SHA-256 at 0° (L1)
	h1 := sha256Image(stdImg)
	if h1 == original.SHA256 {
		return PipelineResult{Level: 1, Match: true, Details: "Normalized Identical"}
	}

	// 3. Compute L3 tile hashes for test image and compare against reference
	testDict, err := computeL3TileDict(stdImg)
	if err != nil {
		return PipelineResult{Level: 3, Match: false, Details: fmt.Sprintf("L3 extraction error: %v", err)}
	}

	// Debug: log reference vs test hashes
	log.Printf("[DEBUG] Reference TileMap %d tiles, Test TileMap %d tiles", len(original.TileMap), len(testDict))
	for key, refHash := range original.TileMap {
		testHash, ok := testDict[key]
		if ok {
			d := hammingMultiWord(refHash, testHash)
			// Log first word only for brevity
			log.Printf("[DEBUG] tile %s: ref=0x%016x... test=0x%016x... total_hamming=%d",
				key, refHash[0], testHash[0], d)
		} else {
			log.Printf("[DEBUG] tile %s: test=MISSING", key)
		}
	}

	sim, tileResults := compareTileDicts(testDict, original.TileMap)

	// Build per-tile detail string
	var tileDetails string
	if tileResults != nil {
		var matched, total int
		for _, ok := range tileResults {
			total++
			if ok {
				matched++
			}
		}
		tileDetails = fmt.Sprintf("%d/%d tiles match (%.1f%%)", matched, total, float64(matched)/float64(total)*100)
	}

	match := sim >= L3_THRESHOLD
	return PipelineResult{Level: 3, Match: match, Details: fmt.Sprintf("Tile-by-tile: %s", tileDetails)}
}

// compareTileDicts compares two tile dictionaries keyed by "col:row".
// Each key in the test dict is looked up in the reference dict and compared
// by Hamming distance. Only keys present in both dicts are compared.
// Returns the fraction of matching tiles and a per-tile result map.
// This catches localized tampering (e.g. head removal) while still
// allowing crops and resolution changes to pass when content matches.
func hammingMultiWord(a, b []uint64) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	total := 0
	for i := 0; i < n; i++ {
		total += bits.OnesCount64(a[i] ^ b[i])
	}
	return total
}

func compareTileDicts(test, ref TileDict) (float64, map[string]bool) {
	results := make(map[string]bool)
	if len(test) == 0 || len(ref) == 0 {
		return 0, results
	}
	matchCount := 0
	totalCount := 0
	for key, testHash := range test {
		refHash, ok := ref[key]
		if !ok {
			continue
		}
		totalCount++
		d := hammingMultiWord(testHash, refHash)
		if d <= L3_HAMMING_THRESHOLD {
			results[key] = true
			matchCount++
		} else {
			results[key] = false
		}
	}
	if totalCount == 0 {
		return 0, results
	}
	return float64(matchCount) / float64(totalCount), results
}

// --- Image Utils ---

func resizeImage(img image.Image, w, h int) image.Image {
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

	mat, err := gocv.ImageToMatRGBA(rgba)
	if err != nil {
		return img
	}
	defer mat.Close()

	resized := gocv.NewMat()
	defer resized.Close()

	// InterpolationArea applies anti-aliasing when downscaling
	gocv.Resize(mat, &resized, image.Point{X: w, Y: h}, 0, 0, gocv.InterpolationArea)

	out, err := resized.ToImage()
	if err != nil {
		return img
	}
	return out
}

// sha256Image computes the SHA-256 hash of the RGBA pixel data of an image.
// This captures both structure and color after normalization.
func sha256Image(img image.Image) [32]byte {
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	return sha256.Sum256(rgba.Pix)
}

// resizeProportional scales the image down proportionally so its area
// does not exceed 3 megapixels (MAX_IMAGE_AREA). Images already smaller
// than that are returned unchanged.
func resizeProportional(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	area := w * h

	if area <= MAX_IMAGE_AREA {
		return img
	}

	scale := math.Sqrt(float64(MAX_IMAGE_AREA) / float64(area))
	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)

	// Ensure at least 1 pixel in each dimension
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	return resizeImage(img, newW, newH)
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
		if loadPCAPlanes("pca_planes.bin") {
			log.Printf("[INFO] Loaded PCA LSH planes")
			return
		}
		rand.Seed(42)
		lshPlanes = make([][]float32, LSH_BITS)
		for i := 0; i < LSH_BITS; i++ {
			lshPlanes[i] = make([]float32, 1000)
			for j := 0; j < 1000; j++ {
				lshPlanes[i][j] = float32(rand.NormFloat64())
			}
		}
		log.Printf("[WARN] Using random LSH planes")
	})
}

func loadPCAPlanes(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(data) < 8 {
		return false
	}
	numBits := int(binary.LittleEndian.Uint32(data[0:4]))
	numDims := int(binary.LittleEndian.Uint32(data[4:8]))
	if numBits != LSH_BITS || numDims != 1000 {
		return false
	}
	expected := 8 + numBits*numDims*4
	if len(data) < expected {
		return false
	}
	lshPlanes = make([][]float32, LSH_BITS)
	off := 8
	for i := 0; i < LSH_BITS; i++ {
		lshPlanes[i] = make([]float32, 1000)
		for j := 0; j < 1000; j++ {
			bits := binary.LittleEndian.Uint32(data[off : off+4])
			lshPlanes[i][j] = math.Float32frombits(bits)
			off += 4
		}
	}
	return true
}

// computeL3TileDict computes SimHashes for all tiles in the image and returns
// a dictionary keyed by "col:row" (grid position) mapping to the multi-word SimHash.
// This dictionary is what gets stored on the blockchain.
func computeL3TileDict(img image.Image) (TileDict, error) {
	initLSH()
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	log.Printf("[DEBUG] computeL3TileDict: image %dx%d, tile_size=%d", width, height, TILE_SIZE)

	dict := make(TileDict)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for y := 0; y <= height-TILE_SIZE; y += TILE_SIZE {
		for x := 0; x <= width-TILE_SIZE; x += TILE_SIZE {
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				tile := cropImage(img, tx, ty, TILE_SIZE, TILE_SIZE)
				inputTensor, err := preprocessForModel(tile)
				if err != nil {
					log.Printf("[DEBUG] preprocess error at tile %d,%d: %v", tx, ty, err)
					return
				}
				features, err := remoteInference(inputTensor)
				if err != nil {
					log.Printf("[DEBUG] inference error at tile %d,%d: %v", tx, ty, err)
					return
				}
				h := computeLSH(features)
				col := tx / TILE_SIZE
				row := ty / TILE_SIZE
				key := fmt.Sprintf("%d:%d", col, row)

				// Debug: log first tile's feature stats and hash
				if col == 0 && row == 0 {
					var sum, sumSq float32
					for _, f := range features[:10] {
						sum += f
						sumSq += f * f
					}
					log.Printf("[DEBUG] tile %s: hash[0]=0x%016x, first10_features_sum=%.4f, first10_sq_sum=%.4f",
						key, h[0], sum, sumSq)
				}

				mu.Lock()
				dict[key] = h
				mu.Unlock()
			}(x, y)
		}
	}

	wg.Wait()
	log.Printf("[DEBUG] computeL3TileDict: %d tiles computed", len(dict))
	return dict, nil
}

func cropImage(img image.Image, x, y, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, image.Point{x, y}, draw.Src)
	return dst
}

func preprocessForModel(img image.Image) ([]float32, error) {
	// 1. Convert to RGBA and resize to 256×256 using OpenCV
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

	mat, err := gocv.ImageToMatRGBA(rgba)
	if err != nil {
		return nil, err
	}
	defer mat.Close()

	resized := gocv.NewMat()
	defer resized.Close()
	gocv.Resize(mat, &resized, image.Point{X: MODEL_INPUT_SIZE, Y: MODEL_INPUT_SIZE}, 0, 0, gocv.InterpolationArea)

	// 2. ImageNet normalization (reading RGBA data directly from the Mat)
	mean := [3]float32{0.485, 0.456, 0.406}
	std := [3]float32{0.229, 0.224, 0.225}

	data := make([]float32, 3*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE)
	for y := 0; y < MODEL_INPUT_SIZE; y++ {
		for x := 0; x < MODEL_INPUT_SIZE; x++ {
			// RGBA layout: each pixel occupies 4 columns (R, G, B, A)
			// GetUCharAt(row, col) treats col as byte offset within the row
			base := x * 4
			r := float32(resized.GetUCharAt(y, base)) / 255.0
			g := float32(resized.GetUCharAt(y, base+1)) / 255.0
			b := float32(resized.GetUCharAt(y, base+2)) / 255.0

			idx := y*MODEL_INPUT_SIZE + x
			data[0*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE+idx] = (r - mean[0]) / std[0]
			data[1*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE+idx] = (g - mean[1]) / std[1]
			data[2*MODEL_INPUT_SIZE*MODEL_INPUT_SIZE+idx] = (b - mean[2]) / std[2]
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

func computeLSH(features []float32) []uint64 {
	// L2 normalize before LSH so that direction (angle) matters, not magnitude.
	// This makes small visual changes more detectable in the SimHash bits.
	var norm float32
	for _, f := range features {
		norm += f * f
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm == 0 {
		norm = 1
	}

	hash := make([]uint64, LSH_WORDS)
	for i := 0; i < LSH_BITS; i++ {
		var dot float32
		for j := 0; j < len(features); j++ {
			dot += (features[j] / norm) * lshPlanes[i][j]
		}
		if dot > 0 {
			word := i / 64
			bit := uint(i % 64)
			hash[word] |= 1 << bit
		}
	}
	return hash
}
