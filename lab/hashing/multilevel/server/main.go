package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"

	ort "github.com/yalue/onnxruntime_go"
)

const MODEL_INPUT_SIZE = 256

var session *ort.AdvancedSession
var inputTensor *ort.Tensor[float32]
var outputTensor *ort.Tensor[float32]

func main() {
	ort.SetSharedLibraryPath("/usr/lib/libonnxruntime.so")
	err := ort.InitializeEnvironment()
	if err != nil {
		log.Fatalf("Failed to initialize ONNX Runtime: %v", err)
	}
	defer ort.DestroyEnvironment()

	// Pre-allocate tensors for this worker
	inputShape := ort.NewShape(1, 3, MODEL_INPUT_SIZE, MODEL_INPUT_SIZE)
	inputTensor, err = ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		log.Fatalf("Failed to create input tensor: %v", err)
	}
	defer inputTensor.Destroy()

	outputShape := ort.NewShape(1, 1000)
	outputTensor, err = ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		log.Fatalf("Failed to create output tensor: %v", err)
	}
	defer outputTensor.Destroy()

	session, err = ort.NewAdvancedSession("model.onnx",
		[]string{"pixel_values"}, []string{"logits"},
		[]ort.ArbitraryTensor{inputTensor}, []ort.ArbitraryTensor{outputTensor}, nil)
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	defer session.Destroy()

	http.HandleFunc("/predict", handlePredict)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("ONNX Server listening on port %s...\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handlePredict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	// Expected size: 1 * 3 * 256 * 256 * 4 bytes
	expectedSize := 1 * 3 * MODEL_INPUT_SIZE * MODEL_INPUT_SIZE * 4
	if len(body) != expectedSize {
		http.Error(w, fmt.Sprintf("Invalid body size. Expected %d, got %d", expectedSize, len(body)), http.StatusBadRequest)
		return
	}

	// Fill input tensor
	inputData := inputTensor.GetData()
	for i := 0; i < len(inputData); i++ {
		bits := binary.LittleEndian.Uint32(body[i*4 : (i+1)*4])
		inputData[i] = math.Float32frombits(bits)
	}

	// Run inference
	err = session.Run()
	if err != nil {
		http.Error(w, "Inference failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return output as binary float32
	outputData := outputTensor.GetData()
	w.Header().Set("Content-Type", "application/octet-stream")
	res := make([]byte, len(outputData)*4)
	for i, f := range outputData {
		binary.LittleEndian.PutUint32(res[i*4:(i+1)*4], math.Float32bits(f))
	}
	w.Write(res)
}
