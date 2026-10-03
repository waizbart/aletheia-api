//go:build integration

package feature_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/dataset/manifest"
	"github.com/waizbart/aletheia-api/internal/dataset/transform"
	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/observability"
	"github.com/waizbart/aletheia-api/internal/testdata"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// tccVerifyTopK mirrors the unexported verifyTopK in internal/usecase/verify.go.
// The verdict itself always comes from the real use case, which applies its own
// constant; this copy only interprets the pre-filter diagnostics. Because the
// constant is unexported, every sample cross-checks it against the candidate
// count the use case actually reported, so the copy cannot drift silently.
const tccVerifyTopK = 64

// tccControlFamily labels the synthetic negative control this experiment adds
// on top of the manifest: a base image queried against the database with its
// own certificate hidden.
//
// The manifest's different_image control cannot answer the question that
// matters for a certifier — "does an image that was never certified match
// somebody else's certificate?" — because it hands back the peer base's bytes
// untouched, and the peer is itself certified, so SHA-256 resolves it before
// any visual matching happens. Hiding the certificate forces the full pHash +
// ORB path to run against the other 992 certificates with no correct answer
// available.
const tccControlFamily = "uncertified_image"

// ---------------------------------------------------------------------------
// In-memory certificate repository
// ---------------------------------------------------------------------------

// tccMemRepo implements usecase.CertificateRepository over memory, reproducing
// the semantics of repository.PostgresCertificateRepo:
//
//   - FindByHash is an exact content-hash lookup.
//   - FindCandidatesByPHashes resolves the LSH pre-filter the same way: every
//     band byte of every candidate rotation probes a (band_idx, band_value)
//     index, the colliding certificates are re-scored with the exact 256-bit
//     Hamming distance, those beyond maxDistance are dropped, and the closest
//     topK are returned in distance order.
//
// One difference is deliberate and declared: Postgres leaves the order of
// candidates tied on distance undefined, while this repository breaks ties on
// content hash so a run is reproducible. Step 4 of the protocol (the e2e test
// against the real database) is what establishes whether that matters.
type tccMemRepo struct {
	mu     sync.RWMutex
	byHash map[string]*domain.Certificate
	// bands[i][v] holds the certificates whose pHash byte i equals v.
	bands []map[byte][]*domain.Certificate
	// hidden is the content hash the current view suppresses, which is how the
	// uncertified_image control is run without mutating shared state.
	hidden string
}

func newTCCMemRepo() *tccMemRepo {
	b := make([]map[byte][]*domain.Certificate, domain.PHashBandCount)
	for i := range b {
		b[i] = make(map[byte][]*domain.Certificate)
	}
	return &tccMemRepo{byHash: make(map[string]*domain.Certificate), bands: b}
}

// hiding returns a view of the repository with one certificate suppressed. The
// underlying maps are shared, so the view is cheap and safe to build per query.
func (r *tccMemRepo) hiding(contentHash string) *tccMemRepo {
	return &tccMemRepo{byHash: r.byHash, bands: r.bands, hidden: contentHash, mu: sync.RWMutex{}}
}

func (r *tccMemRepo) Save(_ context.Context, cert *domain.Certificate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byHash[cert.ContentHash]; ok {
		return fmt.Errorf("already certified: %s", cert.ContentHash)
	}
	if cert.ID == "" {
		cert.ID = cert.ContentHash
	}
	r.byHash[cert.ContentHash] = cert
	if cert.PHash != nil {
		bands := domain.PHashBands(*cert.PHash)
		for i := 0; i < domain.PHashBandCount; i++ {
			r.bands[i][bands[i]] = append(r.bands[i][bands[i]], cert)
		}
	}
	return nil
}

func (r *tccMemRepo) FindByHash(_ context.Context, contentHash string) (*domain.Certificate, error) {
	if contentHash == r.hidden {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byHash[contentHash]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (r *tccMemRepo) Delete(_ context.Context, contentHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byHash[contentHash]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byHash, contentHash)
	return nil
}

func (r *tccMemRepo) FindCandidatesByPHashes(
	_ context.Context, phashes [][32]byte, maxDistance, topK int,
) ([]*domain.Certificate, error) {
	return r.rankCandidates(phashes, maxDistance, topK), nil
}

type tccScored struct {
	cert *domain.Certificate
	dist int
}

// rankCandidates is the shared body of the pre-filter: it is called both by the
// use case (through FindCandidatesByPHashes) and by the diagnostics below with
// topK = 0, which returns the full ranked list so the correct certificate's
// rank can be reported even when it falls outside the top-K window.
func (r *tccMemRepo) rankCandidates(phashes [][32]byte, maxDistance, topK int) []*domain.Certificate {
	if len(phashes) == 0 {
		return nil
	}

	r.mu.RLock()
	seen := make(map[string]*domain.Certificate)
	for _, ph := range phashes {
		bands := domain.PHashBands(ph)
		for i := 0; i < domain.PHashBandCount; i++ {
			for _, c := range r.bands[i][bands[i]] {
				seen[c.ContentHash] = c
			}
		}
	}
	r.mu.RUnlock()

	hits := make([]tccScored, 0, len(seen))
	for _, c := range seen {
		if c.ContentHash == r.hidden || c.PHash == nil {
			continue
		}
		minDist := domain.Hamming256(phashes[0], *c.PHash)
		for _, ph := range phashes[1:] {
			if d := domain.Hamming256(ph, *c.PHash); d < minDist {
				minDist = d
			}
		}
		if minDist > maxDistance {
			continue
		}
		hits = append(hits, tccScored{cert: c, dist: minDist})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].cert.ContentHash < hits[j].cert.ContentHash
	})
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}

	out := make([]*domain.Certificate, len(hits))
	for i, h := range hits {
		out[i] = h.cert
	}
	return out
}

// ---------------------------------------------------------------------------
// Stage-capturing recorder
// ---------------------------------------------------------------------------

// tccRecorder is an observability.Recorder that keeps the stage timings and
// attributes of a single verify call. Using the real port means the experiment
// measures usecase.VerifyUseCase.Execute itself rather than a reimplementation
// of it, so the stage boundaries are exactly the production ones.
type tccRecorder struct {
	verdict observability.Verdict
	stages  map[string]float64
	attrs   map[string]any
}

func newTCCRecorder() *tccRecorder {
	return &tccRecorder{stages: make(map[string]float64, 8), attrs: make(map[string]any, 16)}
}

func (r *tccRecorder) SetPipeline(string)                 {}
func (r *tccRecorder) SetVerdict(v observability.Verdict) { r.verdict = v }
func (r *tccRecorder) ms(name string) float64             { return r.stages[name] }
func (r *tccRecorder) attr(key string) (any, bool)        { v, ok := r.attrs[key]; return v, ok }
func (r *tccRecorder) attrInt(key string) int             { v, _ := r.attrs[key].(int); return v }

func (r *tccRecorder) StartStage(_ context.Context, name string) observability.StageHandle {
	return &tccStage{rec: r, name: name, start: time.Now()}
}

type tccStage struct {
	rec   *tccRecorder
	name  string
	start time.Time
	ended bool
}

func (s *tccStage) SetAttrs(attrs ...observability.Attr) {
	for _, a := range attrs {
		// Only the first value for a key is kept: the per-candidate children all
		// write the same keys, and the first candidate is the one whose metrics
		// the pipeline acted on.
		if _, exists := s.rec.attrs[a.Key]; !exists {
			s.rec.attrs[a.Key] = a.Value
		}
	}
}

func (s *tccStage) Fail(err error) {
	if err != nil {
		s.rec.attrs[s.name+".error"] = err.Error()
	}
}

func (s *tccStage) Skip(reason string) { s.rec.attrs[s.name+".skipped"] = reason }

func (s *tccStage) Child(name string) observability.StageHandle {
	return &tccStage{rec: s.rec, name: s.name + "." + name, start: time.Now()}
}

func (s *tccStage) End() {
	if s.ended {
		return
	}
	s.ended = true
	s.rec.stages[s.name] += float64(time.Since(s.start).Microseconds()) / 1000.0
}

// ---------------------------------------------------------------------------
// Job and result records
// ---------------------------------------------------------------------------

type tccJob struct {
	sampleID   string
	baseID     string
	transform  string
	family     string
	confidence string
	expected   bool
	negControl bool
	// imagePath is the file fed to verify.
	imagePath string
	// isControl marks the synthetic uncertified_image control, for which the
	// base's own certificate is hidden and no pairwise comparison applies.
	isControl bool
}

type tccResult struct {
	job tccJob

	pipelineCertified bool
	pipelineMatched   bool
	certTarget        string // "base" | "other" | "none"
	decidedStage      string

	pairEvaluated bool
	pairMatched   bool
	pairInliers   int
	pairColorMean float64
	pairColorMax  float64
	pairCells     int
	pairCoverage  float64

	candidates   int
	hasCorrect   bool
	correctRank  int
	phashHamming int
	rankedTotal  int

	msSHA256    float64
	msExact     float64
	msPHash     float64
	msORB       float64
	msPrefilter float64
	msMatch     float64
	msTotal     float64

	err error
}

var tccCSVHeader = []string{
	"sample_id", "base_id", "transform", "family", "confidence",
	"expected_match", "is_negative_control", "is_control",
	"pipeline_certified", "pipeline_matched", "cert_target", "decided_stage",
	"pair_evaluated", "pair_matched",
	"pair_inliers", "pair_color_mean", "pair_color_max", "pair_cells", "pair_coverage",
	"prefilter_candidates", "prefilter_ranked_total", "prefilter_has_correct",
	"prefilter_correct_rank", "phash_hamming",
	"ms_sha256", "ms_exact_lookup", "ms_phash", "ms_orb_extract",
	"ms_prefilter", "ms_match", "ms_total",
}

func (r tccResult) row() []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	return []string{
		r.job.sampleID, r.job.baseID, r.job.transform, r.job.family, r.job.confidence,
		strconv.FormatBool(r.job.expected), strconv.FormatBool(r.job.negControl),
		strconv.FormatBool(r.job.isControl),
		strconv.FormatBool(r.pipelineCertified), strconv.FormatBool(r.pipelineMatched),
		r.certTarget, r.decidedStage,
		strconv.FormatBool(r.pairEvaluated), strconv.FormatBool(r.pairMatched),
		strconv.Itoa(r.pairInliers), f(r.pairColorMean), f(r.pairColorMax),
		strconv.Itoa(r.pairCells), f(r.pairCoverage),
		strconv.Itoa(r.candidates), strconv.Itoa(r.rankedTotal),
		strconv.FormatBool(r.hasCorrect), strconv.Itoa(r.correctRank),
		strconv.Itoa(r.phashHamming),
		f(r.msSHA256), f(r.msExact), f(r.msPHash), f(r.msORB),
		f(r.msPrefilter), f(r.msMatch), f(r.msTotal),
	}
}

// ---------------------------------------------------------------------------
// The experiment
// ---------------------------------------------------------------------------

// TestTCC_PipelineSimulation is the primary measurement of the benchmark: it
// certifies every base in the generated manifest into an in-memory database and
// then verifies every sample through usecase.VerifyUseCase.Execute — the same
// code path the HTTP API runs — recording, per sample, the pipeline verdict, the
// isolated pairwise verdict, which stage decided, the matcher metrics, the
// pre-filter diagnostics and the per-stage latency.
//
// The whole point of running the pipeline rather than the matcher alone is the
// pre-filter: a pairwise comparison is handed the correct reference, while the
// pipeline has to find it among every certificate in the database. The
// difference between the two columns is the cost of that search, and the
// prefilter_correct_rank column attributes it.
//
// Environment:
//
//	TCC_OUT      path of the per-sample CSV (required for the CSV to be written)
//	TCC_WORKERS  parallel verify workers (default: NumCPU)
//	TCC_STRIDE   evaluate every Nth sample (default 1; use 20 for latency runs)
//
// The test does not gate: it measures. Accuracy gates live in
// TestDataset_FullMatrix and TestE2E_GeneratedDataset_Matrix.
func TestTCC_PipelineSimulation(t *testing.T) {
	manifestPath := testdata.ManifestPath()
	if _, err := os.Stat(manifestPath); err != nil {
		t.Skipf("no manifest at %s — run: go run -tags datasetgen ./cmd/datasetgen --source picsum --count 1000", manifestPath)
	}
	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	tccRebasePaths(t, m, manifestPath)

	outPath := os.Getenv("TCC_OUT")
	if outPath == "" {
		t.Log("TCC_OUT is unset — running without a CSV; set it to collect per-sample records")
	}
	workers := runtime.NumCPU()
	if v := os.Getenv("TCC_WORKERS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			workers = n
		}
	}
	stride := 1
	if v := os.Getenv("TCC_STRIDE"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			stride = n
		}
	}

	t.Logf("manifest %s: %d bases, %d samples, generator %s, run %s",
		manifestPath, m.Metadata.BaseCount, m.Metadata.SampleCount,
		m.Metadata.GeneratorVersion, m.Metadata.RunID)
	t.Logf("thresholds in manifest: %+v", m.Metadata.Thresholds)
	t.Logf("thresholds in code    : min_inliers=%d max_color_mean=%.1f max_cell_dist=%.1f min_area_coverage=%.2f max_phash_distance=%d top_k=%d",
		domain.MinInliers, domain.MaxColorMean, domain.MaxCellDist,
		domain.MinAreaCoverage, domain.MaxPHashDistance, tccVerifyTopK)
	t.Logf("workers=%d stride=%d out=%q", workers, stride, outPath)

	if m.Metadata.Thresholds != manifest.ActiveThresholds() {
		t.Fatalf("manifest thresholds %+v differ from the live constants %+v — "+
			"the labels were assigned under different thresholds, so the dataset must be regenerated before measuring",
			m.Metadata.Thresholds, manifest.ActiveThresholds())
	}

	ctx := t.Context()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	repo := newTCCMemRepo()

	// --- Phase 1: certify every base ---------------------------------------

	type tccBase struct {
		id   string
		path string
	}
	seen := make(map[string]bool, m.Metadata.BaseCount)
	var bases []tccBase
	for _, s := range m.Samples {
		if !seen[s.BaseImageID] {
			seen[s.BaseImageID] = true
			bases = append(bases, tccBase{id: s.BaseImageID, path: s.SourcePath})
		}
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i].id < bases[j].id })

	certifyStart := time.Now()
	baseHash := make(map[string]string, len(bases))
	certifyFailed := 0
	for _, b := range bases {
		img, rerr := os.ReadFile(b.path)
		if rerr != nil {
			t.Errorf("base %s: read %q: %v", b.id, b.path, rerr)
			certifyFailed++
			continue
		}
		// Mirror usecase.CertifyUseCase: hash, pHash, ORB signature (non-fatal
		// on failure), feature commitment, save.
		hash, _ := domain.HashContent(bytes.NewReader(img))
		phash := domain.PHash256(img)
		var sig *domain.FeatureSignature
		if phash != nil {
			if s, cerr := ext.Compute(ctx, img); cerr == nil {
				sig = s
			} else {
				t.Logf("base %s: ORB extraction failed, certifying pHash-only: %v", b.id, cerr)
			}
		}
		commitment := domain.FeatureCommitment(phash, sig)
		cert := &domain.Certificate{
			ID:                b.id,
			ContentHash:       hash,
			PHash:             phash,
			Signature:         sig,
			FeatureCommitment: &commitment,
			Registrant:        "tcc-benchmark",
			CreatedAt:         time.Now().UTC(),
		}
		if serr := repo.Save(ctx, cert); serr != nil {
			t.Errorf("base %s: save: %v", b.id, serr)
			certifyFailed++
			continue
		}
		baseHash[b.id] = hash
	}
	if certifyFailed > 0 {
		t.Fatalf("certified %d/%d bases (%d failed) — aborting", len(baseHash), len(bases), certifyFailed)
	}
	t.Logf("certified %d bases in %s", len(baseHash), time.Since(certifyStart).Round(time.Second))

	// --- Phase 2: build the job list ---------------------------------------

	samples := make([]manifest.Sample, len(m.Samples))
	copy(samples, m.Samples)
	sort.Slice(samples, func(i, j int) bool { return samples[i].ID < samples[j].ID })

	var jobs []tccJob
	for i, s := range samples {
		if i%stride != 0 {
			continue
		}
		jobs = append(jobs, tccJob{
			sampleID:   s.ID,
			baseID:     s.BaseImageID,
			transform:  tccTransformName(s.ID),
			family:     s.TransformFamily,
			confidence: s.Confidence,
			expected:   s.ExpectedMatch,
			negControl: s.IsNegControl,
			imagePath:  s.OutputPath,
		})
	}
	// One uncertified_image control per base: the base's own bytes queried with
	// its certificate hidden. Iterating every base covers exactly the same set
	// of images the manifest's cyclic different_image pairing covers, so the
	// control is the same population seen without a correct answer available.
	for i, b := range bases {
		if i%stride != 0 {
			continue
		}
		jobs = append(jobs, tccJob{
			sampleID:   b.id + "__" + tccControlFamily,
			baseID:     b.id,
			transform:  tccControlFamily,
			family:     tccControlFamily,
			confidence: string(transform.ConfidenceHigh),
			expected:   false,
			negControl: true,
			imagePath:  b.path,
			isControl:  true,
		})
	}
	controls := 0
	for i := range bases {
		if i%stride == 0 {
			controls++
		}
	}
	t.Logf("evaluating %d jobs (%d manifest samples + %d controls)",
		len(jobs), len(jobs)-controls, controls)

	// --- Phase 3: verify ---------------------------------------------------

	jobCh := make(chan tccJob, workers*4)
	resCh := make(chan tccResult, workers*4)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				resCh <- tccRunJob(ctx, ext, repo, baseHash, j)
			}
		}()
	}
	go func() {
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
	}()
	go func() { wg.Wait(); close(resCh) }()

	var csvW *csv.Writer
	if outPath != "" {
		if mkErr := os.MkdirAll(filepath.Dir(outPath), 0o755); mkErr != nil {
			t.Fatalf("mkdir for TCC_OUT: %v", mkErr)
		}
		f, cerr := os.Create(outPath)
		if cerr != nil {
			t.Fatalf("create TCC_OUT %q: %v", outPath, cerr)
		}
		defer f.Close()
		csvW = csv.NewWriter(f)
		if werr := csvW.Write(tccCSVHeader); werr != nil {
			t.Fatalf("write CSV header: %v", werr)
		}
		defer csvW.Flush()
	}

	start := time.Now()
	var done, errs int
	var pipeTP, pipeFP, pipeFN, pipeTN int
	var pairTP, pairFP, pairFN, pairTN int
	var ctrlMatched, ctrlTotal int
	var wrongCert int
	stageTally := map[string]int{}

	for r := range resCh {
		done++
		if r.err != nil {
			errs++
			if errs <= 20 {
				t.Logf("sample %s: %v", r.job.sampleID, r.err)
			}
		}
		if csvW != nil {
			if werr := csvW.Write(r.row()); werr != nil {
				t.Fatalf("write CSV row: %v", werr)
			}
			if done%2000 == 0 {
				csvW.Flush()
			}
		}

		stageTally[r.decidedStage]++
		if r.certTarget == "other" {
			wrongCert++
		}

		if r.job.isControl {
			ctrlTotal++
			if r.pipelineMatched || r.certTarget == "other" {
				ctrlMatched++
			}
		} else {
			if r.job.expected {
				if r.pipelineMatched {
					pipeTP++
				} else {
					pipeFN++
				}
			} else {
				if r.pipelineMatched {
					pipeFP++
				} else {
					pipeTN++
				}
			}
			if r.pairEvaluated {
				if r.job.expected {
					if r.pairMatched {
						pairTP++
					} else {
						pairFN++
					}
				} else {
					if r.pairMatched {
						pairFP++
					} else {
						pairTN++
					}
				}
			}
		}

		if done%1000 == 0 {
			elapsed := time.Since(start)
			per := elapsed / time.Duration(done)
			t.Logf("progress %d/%d  elapsed=%s  per-sample=%s  eta=%s",
				done, len(jobs), elapsed.Round(time.Second), per.Round(time.Millisecond),
				(per * time.Duration(len(jobs)-done)).Round(time.Second))
		}
	}

	if csvW != nil {
		csvW.Flush()
		if ferr := csvW.Error(); ferr != nil {
			t.Fatalf("flush CSV: %v", ferr)
		}
		t.Logf("per-sample records written to %s", outPath)
	}

	// --- Phase 4: console summary ------------------------------------------
	//
	// The authoritative tables come from scripts/tcc_summary.py over the CSV.
	// What follows is a sanity read-out so an interrupted run is still legible.

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n=== TCC PIPELINE SIMULATION ===\n")
	fmt.Fprintf(&sb, "jobs evaluated : %d in %s (%d workers, stride %d)\n",
		done, time.Since(start).Round(time.Second), workers, stride)
	fmt.Fprintf(&sb, "errors         : %d\n", errs)
	fmt.Fprintf(&sb, "pipeline (manifest samples): TP=%d FP=%d FN=%d TN=%d  precision=%.3f recall=%.3f\n",
		pipeTP, pipeFP, pipeFN, pipeTN,
		tccSafeDiv(pipeTP, pipeTP+pipeFP), tccSafeDiv(pipeTP, pipeTP+pipeFN))
	fmt.Fprintf(&sb, "pairwise (manifest samples): TP=%d FP=%d FN=%d TN=%d  precision=%.3f recall=%.3f\n",
		pairTP, pairFP, pairFN, pairTN,
		tccSafeDiv(pairTP, pairTP+pairFP), tccSafeDiv(pairTP, pairTP+pairFN))
	fmt.Fprintf(&sb, "uncertified_image control  : %d/%d matched some certificate\n", ctrlMatched, ctrlTotal)
	fmt.Fprintf(&sb, "attributions to a wrong certificate: %d\n", wrongCert)
	fmt.Fprintln(&sb, "\ndeciding stage:")
	stages := make([]string, 0, len(stageTally))
	for s := range stageTally {
		stages = append(stages, s)
	}
	sort.Strings(stages)
	for _, s := range stages {
		fmt.Fprintf(&sb, "  %-24s %d\n", s, stageTally[s])
	}
	t.Log(sb.String())

	if errs > 0 {
		t.Errorf("%d samples could not be evaluated", errs)
	}
}

// tccRunJob verifies one sample through the production use case and, for
// manifest samples, also runs the isolated pairwise comparison against the
// correct base.
func tccRunJob(
	ctx context.Context,
	ext *feature.OpenCVExtractor,
	repo *tccMemRepo,
	baseHash map[string]string,
	j tccJob,
) tccResult {
	res := tccResult{job: j, correctRank: -1, phashHamming: -1}

	img, err := os.ReadFile(j.imagePath)
	if err != nil {
		res.err = fmt.Errorf("read %q: %w", j.imagePath, err)
		res.decidedStage = "read_error"
		return res
	}

	wantHash, ok := baseHash[j.baseID]
	if !ok {
		res.err = fmt.Errorf("no certified hash for base %s", j.baseID)
		res.decidedStage = "missing_base"
		return res
	}

	// The control runs against a view with the base's own certificate hidden,
	// which is what makes the image "never certified" for this query.
	view := repo
	if j.isControl {
		view = repo.hiding(wantHash)
	}

	rec := newTCCRecorder()
	vctx := observability.WithRecorder(ctx, rec)
	uc := usecase.NewVerifyUseCase(view, ext)

	t0 := time.Now()
	out, verr := uc.Execute(vctx, usecase.VerifyInput{Content: bytes.NewReader(img)})
	res.msTotal = float64(time.Since(t0).Microseconds()) / 1000.0
	if verr != nil {
		res.err = fmt.Errorf("verify: %w", verr)
		res.decidedStage = "verify_error"
		return res
	}

	res.msSHA256 = rec.ms("sha256")
	res.msExact = rec.ms("exact_lookup")
	res.msPHash = rec.ms("phash_variants")
	res.msORB = rec.ms("orb_extract")
	res.msPrefilter = rec.ms("lsh_prefilter")
	res.msMatch = rec.ms("candidate_matching")
	res.candidates = rec.attrInt("candidates")

	res.pipelineCertified = out.Certified
	switch {
	case !out.Certified || out.Certificate == nil:
		res.certTarget = "none"
	case out.Certificate.ContentHash == wantHash:
		res.certTarget = "base"
		res.pipelineMatched = true
	default:
		res.certTarget = "other"
	}
	res.decidedStage = tccDecidedStage(rec, out)

	// Pre-filter diagnostics: where the correct certificate ranked, whether it
	// survived the Hamming cut at all, and its pHash distance. For the control
	// there is no correct certificate by construction.
	if !j.isControl {
		if variants := domain.PHash256Variants(img); len(variants) > 0 {
			ranked := view.rankCandidates(variants, domain.MaxPHashDistance, 0)
			res.rankedTotal = len(ranked)

			// Drift check on the mirrored top-K: the use case cut the same
			// ranked list down to its own constant, so whenever the list was
			// longer than the window the reported candidate count must be
			// exactly the window size.
			if res.rankedTotal > tccVerifyTopK && res.candidates != tccVerifyTopK {
				res.err = fmt.Errorf(
					"top-K mismatch: %d candidates ranked, use case reported %d, tccVerifyTopK=%d — "+
						"verifyTopK in internal/usecase/verify.go changed and the prefilter diagnostics are no longer comparable",
					res.rankedTotal, res.candidates, tccVerifyTopK)
			}
			for i, c := range ranked {
				if c.ContentHash == wantHash {
					res.hasCorrect = true
					res.correctRank = i
					break
				}
			}
			if cert, _ := view.FindByHash(ctx, wantHash); cert != nil && cert.PHash != nil {
				best := domain.Hamming256(variants[0], *cert.PHash)
				for _, v := range variants[1:] {
					if d := domain.Hamming256(v, *cert.PHash); d < best {
						best = d
					}
				}
				res.phashHamming = best
			}
		}

		// Isolated pairwise comparison: the correct reference is handed over
		// directly, with no search. This is the TestDataset_FullMatrix measure.
		if cert, _ := view.FindByHash(ctx, wantHash); cert != nil && cert.Signature != nil {
			if candSig, cerr := ext.Compute(ctx, img); cerr == nil {
				if dec, merr := ext.Match(ctx, cert.Signature, candSig, img); merr == nil {
					res.pairEvaluated = true
					res.pairMatched = dec.Matched
					res.pairInliers = dec.Inliers
					res.pairColorMean = dec.ColorMean
					res.pairColorMax = dec.ColorMax
					res.pairCells = dec.Cells
					res.pairCoverage = dec.Coverage
				}
			}
		}
	}

	return res
}

// tccDecidedStage names the stage that produced the verdict, reading the same
// verdict detail the API surfaces.
func tccDecidedStage(rec *tccRecorder, out *usecase.VerifyOutput) string {
	if out.Certified {
		if via, ok := rec.verdict.Detail["via"]; ok {
			switch via {
			case "sha256":
				return "sha256"
			case "similaridade visual":
				return "visual_match"
			}
		}
		return "match"
	}
	if reason, ok := rec.verdict.Detail["reason"]; ok {
		switch reason {
		case "imagem não decodificável":
			return "phash_undecodable"
		case "falha na extração de features":
			return "orb_extract_failed"
		}
	}
	if _, ok := rec.attr("candidates"); !ok {
		return "no_match"
	}
	if rec.attrInt("candidates") == 0 {
		return "prefilter_empty"
	}
	return "no_match"
}

// tccRebasePaths makes the manifest's absolute paths usable from here. The
// generator records absolute paths, so a manifest produced under a different
// mount prefix (a container's /work, say) needs its paths reattached to the
// repository root before anything can be read.
func tccRebasePaths(t *testing.T, m *manifest.Manifest, manifestPath string) {
	t.Helper()
	root, err := testdata.Root()
	if err != nil {
		t.Fatalf("testdata root: %v", err)
	}
	manifestDir := filepath.Dir(manifestPath)
	rebased := 0
	rebase := func(p string) string {
		if p == "" {
			return p
		}
		if _, serr := os.Stat(p); serr == nil {
			return p
		}
		candidate := filepath.Join(manifestDir, filepath.Base(filepath.Dir(p)), filepath.Base(p))
		if _, serr := os.Stat(candidate); serr == nil {
			rebased++
			return candidate
		}
		if idx := strings.Index(p, "testdata"); idx >= 0 {
			alt := filepath.Join(root, p[idx:])
			if _, serr := os.Stat(alt); serr == nil {
				rebased++
				return alt
			}
		}
		return p
	}
	for i := range m.Samples {
		m.Samples[i].SourcePath = rebase(m.Samples[i].SourcePath)
		m.Samples[i].OutputPath = rebase(m.Samples[i].OutputPath)
	}
	if rebased > 0 {
		t.Logf("rebased %d manifest paths onto %s", rebased, root)
	}
}

// tccTransformName recovers the transform name from a manifest sample ID, which
// the generator forms as "<base_id>__<transform_name>".
func tccTransformName(sampleID string) string {
	if i := strings.Index(sampleID, "__"); i >= 0 {
		return sampleID[i+2:]
	}
	return sampleID
}

func tccSafeDiv(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
