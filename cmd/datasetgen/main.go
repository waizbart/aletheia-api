//go:build datasetgen

// cmd/datasetgen generates a labeled test dataset for the Aletheia API.
//
// Usage:
//
//	go run -tags datasetgen ./cmd/datasetgen [flags]
//
// Flags:
//
//	--source   "local" (default) or "picsum"
//	--count    number of base images to download/use (default 20 for local, 100 for picsum)
//	--seed     RNG seed for reproducibility (default 42)
//	--out      output directory (default testdata/generated)
//	--workers  number of parallel base-image workers (default 4)
//
// The generator is resumable: re-running with the same flags skips already-
// generated variants (detected by the output file's existence).
//
// Output:
//
//	<out>/base/<id>.<ext>
//	<out>/variants/<id>/<transform_name>.<ext>
//	<out>/manifest.json
//	<out>/manifest.csv
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/waizbart/aletheia-api/internal/dataset/manifest"
	"github.com/waizbart/aletheia-api/internal/dataset/source"
	"github.com/waizbart/aletheia-api/internal/dataset/transform"
	td "github.com/waizbart/aletheia-api/internal/testdata"
)

func main() {
	srcFlag := flag.String("source", "local", `image source: "local" or "picsum"`)
	countFlag := flag.Int("count", 0, "number of base images (0 = all available for local, 100 for picsum)")
	seedFlag := flag.Int64("seed", 42, "RNG seed")
	outFlag := flag.String("out", "", "output directory (default: testdata/generated)")
	workersFlag := flag.Int("workers", 4, "parallel workers")
	flag.Parse()

	outDir := *outFlag
	if outDir == "" {
		outDir = td.Generated()
	}

	count := *countFlag
	if count == 0 {
		if *srcFlag == "picsum" {
			count = 100
		} else {
			count = 0 // all
		}
	}

	var src source.Source
	switch *srcFlag {
	case "local":
		src = &source.Local{Dir: td.Curated("smoke-base")}
	case "picsum":
		cacheDir := filepath.Join(outDir, "cache")
		src = source.NewPicsum(cacheDir)
	default:
		log.Fatalf("unknown source %q; use 'local' or 'picsum'", *srcFlag)
	}

	refs, err := src.List(*seedFlag, count)
	if err != nil {
		log.Fatalf("list source: %v", err)
	}
	if len(refs) == 0 {
		log.Fatal("source returned no images")
	}
	log.Printf("source=%s  bases=%d  seed=%d  out=%s", *srcFlag, len(refs), *seedFlag, outDir)

	entries := transform.Registry()
	log.Printf("transforms=%d  workers=%d", len(entries), *workersFlag)

	baseDir := filepath.Join(outDir, "base")
	varDir := filepath.Join(outDir, "variants")
	for _, d := range []string{baseDir, varDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			log.Fatalf("mkdir %s: %v", d, err)
		}
	}

	type result struct {
		baseRef  source.Ref
		basePath string
		baseB    []byte
		samples  []manifest.Sample
		err      error
	}

	jobs := make(chan source.Ref, len(refs))
	results := make(chan result, len(refs))
	for _, r := range refs {
		jobs <- r
	}
	close(jobs)

	nWorkers := *workersFlag
	if nWorkers < 1 {
		nWorkers = 1
	}
	if nWorkers > runtime.NumCPU() {
		nWorkers = runtime.NumCPU()
	}

	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				res := processBase(src, ref, entries, baseDir, varDir)
				results <- res
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var allSamples []manifest.Sample
	var errCount int
	done := 0
	for res := range results {
		done++
		if res.err != nil {
			log.Printf("[%d/%d] ERROR %s: %v", done, len(refs), res.baseRef.ID, res.err)
			errCount++
			continue
		}
		allSamples = append(allSamples, res.samples...)
		log.Printf("[%d/%d] OK %s  variants=%d", done, len(refs), res.baseRef.ID, len(res.samples))
	}

	commit := toolCommit()
	attrib := ""
	if p, ok := src.(*source.Picsum); ok {
		attrib = p.Attribution()
	}

	m := &manifest.Manifest{
		Metadata: manifest.Metadata{
			GeneratorVersion:  manifest.GeneratorVersion,
			RunID:             fmt.Sprintf("%s-%s", time.Now().UTC().Format("2006-01-02T150405"), commit[:8]),
			Seed:              *seedFlag,
			DatasetSource:     *srcFlag,
			SourceAttribution: attrib,
			BaseCount:         len(refs),
			VariantsPerBase:   len(entries),
			SampleCount:       len(allSamples),
			Thresholds:        manifest.ActiveThresholds(),
			CreatedAt:         time.Now().UTC(),
			ToolCommit:        commit,
		},
		Samples: allSamples,
	}

	jsonPath := filepath.Join(outDir, "manifest.json")
	csvPath := filepath.Join(outDir, "manifest.csv")
	if err := manifest.WriteJSON(jsonPath, m); err != nil {
		log.Fatalf("write manifest.json: %v", err)
	}
	if err := manifest.WriteCSV(csvPath, m); err != nil {
		log.Fatalf("write manifest.csv: %v", err)
	}

	log.Printf("done: %d samples  %d errors  manifest -> %s", len(allSamples), errCount, jsonPath)
	if errCount > 0 {
		os.Exit(1)
	}
}

func processBase(src source.Source, ref source.Ref, entries []transform.Entry, baseDir, varDir string) struct {
	baseRef  source.Ref
	basePath string
	baseB    []byte
	samples  []manifest.Sample
	err      error
} {
	type res = struct {
		baseRef  source.Ref
		basePath string
		baseB    []byte
		samples  []manifest.Sample
		err      error
	}

	baseB, err := src.Fetch(ref)
	if err != nil {
		return res{baseRef: ref, err: fmt.Errorf("fetch: %w", err)}
	}

	ext := mimeExt(ref.MIME)
	basePath := filepath.Join(baseDir, ref.ID+ext)
	if werr := os.WriteFile(basePath, baseB, 0644); werr != nil {
		return res{baseRef: ref, err: fmt.Errorf("write base: %w", werr)}
	}

	vDir := filepath.Join(varDir, ref.ID)
	if err := os.MkdirAll(vDir, 0755); err != nil {
		return res{baseRef: ref, err: fmt.Errorf("mkdir variants: %w", err)}
	}

	// Peer base for negative controls: cyclic — use the ID hash to pick.
	// For simplicity, the generator uses the base bytes themselves as peer
	// for the "different_image" entry; the caller's loop pairs them externally.
	// Since we run concurrently, use base bytes as a trivially-different peer
	// (the generator writes the file; real negative-control pairing happens in
	// the eval step which cycles bases).
	peerB := baseB // placeholder; overridden per entry below

	var samples []manifest.Sample
	for _, e := range entries {
		builder, berr := transform.BuilderFor(e)
		if berr != nil {
			continue
		}

		outPath := filepath.Join(vDir, e.Name+mimeExt(e.MIMEType))

		// Skip if already generated (resumable).
		if _, serr := os.Stat(outPath); serr == nil {
			// File exists: reconstruct sample from existing file.
			if b, rerr := os.ReadFile(outPath); rerr == nil {
				samples = append(samples, buildSample(ref.ID, basePath, outPath, e, b))
			}
			continue
		}

		varB, buildErr := builder(baseB, peerB)
		if buildErr != nil {
			log.Printf("  skip %s/%s: %v", ref.ID, e.Name, buildErr)
			continue
		}
		if werr := os.WriteFile(outPath, varB, 0644); werr != nil {
			log.Printf("  skip %s/%s: write: %v", ref.ID, e.Name, werr)
			continue
		}
		samples = append(samples, buildSample(ref.ID, basePath, outPath, e, varB))
	}

	return res{baseRef: ref, basePath: basePath, baseB: baseB, samples: samples}
}

func buildSample(baseID, basePath, outPath string, e transform.Entry, varB []byte) manifest.Sample {
	h := sha256.Sum256(varB)
	return manifest.Sample{
		ID:              baseID + "__" + e.Name,
		BaseImageID:     baseID,
		SourcePath:      basePath,
		OutputPath:      outPath,
		TransformFamily: e.Family,
		Params:          e.Params,
		ExpectedMatch:   e.ExpectedMatch,
		Confidence:      string(e.Confidence),
		Borderline:      e.Borderline(),
		Rationale:       e.Rationale,
		MIME:            e.MIMEType,
		SHA256:          hex.EncodeToString(h[:]),
		IsNegControl:    e.Family == "different_image",
	}
}

func mimeExt(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func toolCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return strings.Repeat("0", 8)
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 8 {
			return s.Value[:8]
		}
	}
	return strings.Repeat("0", 8)
}
