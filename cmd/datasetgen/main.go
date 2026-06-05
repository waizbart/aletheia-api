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
	// Always use absolute paths so manifest entries are CWD-independent.
	if abs, err := filepath.Abs(outDir); err == nil {
		outDir = abs
	} else {
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

	// Pre-fetch ALL base images first so we can assign correct cyclic peers.
	// This is necessary for the different_image transform to use a genuinely
	// different image as the peer rather than the base itself.
	type baseData struct {
		ref      source.Ref
		basePath string
		bytes    []byte
	}
	allBases := make([]baseData, 0, len(refs))
	log.Printf("pre-fetching %d base images...", len(refs))
	for _, ref := range refs {
		b, ferr := src.Fetch(ref)
		if ferr != nil {
			log.Printf("skip base %s: fetch: %v", ref.ID, ferr)
			continue
		}
		ext := mimeExt(ref.MIME)
		basePath := filepath.Join(baseDir, ref.ID+ext)
		if werr := os.WriteFile(basePath, b, 0644); werr != nil {
			log.Printf("skip base %s: write: %v", ref.ID, werr)
			continue
		}
		allBases = append(allBases, baseData{ref: ref, basePath: basePath, bytes: b})
	}
	log.Printf("pre-fetched %d/%d bases", len(allBases), len(refs))
	if len(allBases) == 0 {
		log.Fatal("no bases available after pre-fetch")
	}

	// Distribute work concurrently, passing the peer bytes explicitly.
	type job struct {
		idx  int
		base baseData
		peer baseData
	}
	type result struct {
		idx     int
		baseID  string
		samples []manifest.Sample
		err     error
	}

	jobs := make(chan job, len(allBases))
	results := make(chan result, len(allBases))
	for i, b := range allBases {
		peer := allBases[(i+1)%len(allBases)]
		jobs <- job{idx: i, base: b, peer: peer}
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
			for j := range jobs {
				samples, jerr := processBase(j.base.ref, j.base.basePath, j.base.bytes,
					j.peer.bytes, entries, varDir)
				results <- result{idx: j.idx, baseID: j.base.ref.ID, samples: samples, err: jerr}
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
			log.Printf("[%d/%d] ERROR %s: %v", done, len(allBases), res.baseID, res.err)
			errCount++
			continue
		}
		allSamples = append(allSamples, res.samples...)
		log.Printf("[%d/%d] OK %s  variants=%d", done, len(allBases), res.baseID, len(res.samples))
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
			BaseCount:         len(allBases),
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

// processBase generates all transform variants for one base image.
// peerB is the bytes of the NEXT base in cyclic order, used by different_image.
func processBase(
	ref source.Ref, basePath string, baseB, peerB []byte,
	entries []transform.Entry, varDir string,
) ([]manifest.Sample, error) {
	vDir := filepath.Join(varDir, ref.ID)
	if err := os.MkdirAll(vDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir variants: %w", err)
	}

	var samples []manifest.Sample
	for _, e := range entries {
		builder, berr := transform.BuilderFor(e)
		if berr != nil {
			continue
		}

		outPath := filepath.Join(vDir, e.Name+mimeExt(e.MIMEType))

		// Resumable: skip if output already exists.
		if _, serr := os.Stat(outPath); serr == nil {
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
	return samples, nil
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
