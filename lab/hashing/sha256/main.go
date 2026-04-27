package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const ORIGINAL_FILE_NAME = "aletheia.jpg"

func main() {
	testdataDir := filepath.Join("..", "testdata")

	originalPath := filepath.Join(testdataDir, ORIGINAL_FILE_NAME)
	originalHash, err := hashFile(originalPath)
	if err != nil {
		log.Fatalf("failed to hash original: %v", err)
	}

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		log.Fatalf("failed to read testdata dir: %v", err)
	}

	fmt.Printf("Original: %s\n", originalPath)
	fmt.Printf("SHA-256:  %x\n\n", originalHash)
	fmt.Println(strings.Repeat("-", 70))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		path := filepath.Join(testdataDir, filename)
		h, err := hashFile(path)
		if err != nil {
			fmt.Printf("%-35s  ERROR: %v\n", filename, err)
			continue
		}

		match := "MISMATCH"
		if h == originalHash {
			match = "MATCH"
		}

		if filename == ORIGINAL_FILE_NAME {
			filename += " (original)"
		}

		fmt.Printf("%-30s  %-10s  %x\n", filename, match, h)
	}
}

func hashFile(path string) ([32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}
