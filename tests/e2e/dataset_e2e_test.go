//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/waizbart/aletheia-api/internal/dataset/manifest"
	"github.com/waizbart/aletheia-api/internal/dataset/transform"
	testdatapkg "github.com/waizbart/aletheia-api/internal/testdata"
)

// TestE2E_GeneratedDataset_Matrix drives the live API (POST /certificates +
// POST /certificates/verify) against every sample in the generated manifest
// and reports a per-family confusion matrix.
//
// A predicted match is only counted as true when:
//
//	certified=true AND matched cert.content_hash == base image's hash
//
// This correctly handles negative controls: peer B is itself certified, so
// verifying B returns certified=true with B's own cert — which is NOT a match
// against base A and counts as a correct true-negative.
//
// The test skips when no manifest is found at testdata.ManifestPath().
// Generate one first:
//
//	go run -tags datasetgen ./cmd/datasetgen --source local
func TestE2E_GeneratedDataset_Matrix(t *testing.T) {
	manifestPath := testdatapkg.ManifestPath()
	if _, err := os.Stat(manifestPath); err != nil {
		t.Skipf("no manifest at %s — run: go run -tags datasetgen ./cmd/datasetgen --source local", manifestPath)
	}

	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	t.Logf("manifest: %d samples over %d bases", m.Metadata.SampleCount, m.Metadata.BaseCount)

	// Manifest paths may have been written with a different absolute prefix
	// (e.g. /work/... inside Docker vs /home/.../aletheia-api/... on the host).
	// Remap them: replace any non-existent prefix with the repo root.
	root, _ := testdatapkg.Root()
	manifestDir := filepath.Dir(manifestPath)
	rebase := func(p string) string {
		if p == "" {
			return p
		}
		if _, err := os.Stat(p); err == nil {
			return p // path is valid as-is
		}
		// Try interpreting p relative to the manifest directory.
		rel := filepath.Base(filepath.Dir(p))
		candidate := filepath.Join(manifestDir, rel, filepath.Base(p))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// Walk from the generated root: strip everything before "testdata/" and
		// reattach to the repo root. Handles /work/testdata/... → <root>/testdata/...
		const marker = "testdata"
		if idx := strings.Index(p, marker); idx >= 0 {
			rebased := filepath.Join(root, p[idx:])
			if _, err := os.Stat(rebased); err == nil {
				return rebased
			}
		}
		return p // return original; ReadFile will surface the error
	}
	for i := range m.Samples {
		m.Samples[i].SourcePath = rebase(m.Samples[i].SourcePath)
		m.Samples[i].OutputPath = rebase(m.Samples[i].OutputPath)
	}

	env := setupE2E(t)
	defer env.cleanup()

	// --- Phase 1: certify all unique bases ------------------------------------

	// Collect unique base source paths.
	type baseInfo struct {
		id         string
		sourcePath string
	}
	seenBases := make(map[string]bool)
	var bases []baseInfo
	for _, s := range m.Samples {
		if !seenBases[s.BaseImageID] {
			seenBases[s.BaseImageID] = true
			bases = append(bases, baseInfo{id: s.BaseImageID, sourcePath: s.SourcePath})
		}
	}

	// certifiedHash maps base_image_id → content_hash returned by the API.
	certifiedHash := make(map[string]string, len(bases))
	certifyFailed := 0
	for _, b := range bases {
		img, rerr := os.ReadFile(b.sourcePath)
		if rerr != nil {
			t.Logf("skip base %s: read: %v", b.id, rerr)
			certifyFailed++
			continue
		}
		status, body := postCertify(t, env, filepath.Base(b.sourcePath), "image/jpeg", img, "datasetgen")
		switch status {
		case http.StatusCreated:
			var cr certResp
			if jerr := json.Unmarshal(body, &cr); jerr == nil {
				certifiedHash[b.id] = cr.ContentHash
			}
		case http.StatusConflict:
			// Already certified (409) — resolve hash from SHA-256 of the file.
			h := sha256.Sum256(img)
			certifiedHash[b.id] = hex.EncodeToString(h[:])
		default:
			t.Logf("certify %s: unexpected status %d", b.id, status)
			certifyFailed++
		}
	}
	t.Logf("certified %d/%d bases (%d failed)", len(certifiedHash), len(bases), certifyFailed)

	// --- Phase 2: concurrent verify of all variants ---------------------------

	verifyWorkers := 8
	if v := os.Getenv("DATASET_E2E_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			verifyWorkers = n
		}
	}
	t.Logf("verifying %d samples with %d workers", len(m.Samples), verifyWorkers)

	type verifyJob struct {
		sample   manifest.Sample
		baseHash string
	}
	type verifyRes struct {
		sample         manifest.Sample
		predictedMatch bool
		statusCode     int
		certified      bool
	}

	jobsCh := make(chan verifyJob, len(m.Samples))
	resultsCh := make(chan verifyRes, len(m.Samples))

	for _, s := range m.Samples {
		if bh, ok := certifiedHash[s.BaseImageID]; ok {
			jobsCh <- verifyJob{sample: s, baseHash: bh}
		}
	}
	close(jobsCh)

	var vwg sync.WaitGroup
	for i := 0; i < verifyWorkers; i++ {
		vwg.Add(1)
		go func() {
			defer vwg.Done()
			for j := range jobsCh {
				img, rerr := os.ReadFile(j.sample.OutputPath)
				if rerr != nil {
					continue
				}
				status, body := postVerifyFile(t, env,
					filepath.Base(j.sample.OutputPath), j.sample.MIME, img)
				var vr verifyResp
				_ = json.Unmarshal(body, &vr)
				predictedMatch := status == http.StatusOK &&
					vr.Certified &&
					vr.Certificate != nil &&
					vr.Certificate.ContentHash == j.baseHash
				resultsCh <- verifyRes{
					sample:         j.sample,
					predictedMatch: predictedMatch,
					statusCode:     status,
					certified:      vr.Certified,
				}
			}
		}()
	}
	go func() { vwg.Wait(); close(resultsCh) }()

	cells := make(map[string]*familyCell)
	var totalTP, totalFP, totalTN, totalFN int
	var highTP, highFP, highTN, highFN int
	var borderlineTP, borderlineFP, borderlineTN, borderlineFN int

	for res := range resultsCh {
		s := res.sample
		predictedMatch := res.predictedMatch
		correct := predictedMatch == s.ExpectedMatch

		c := cells[s.TransformFamily]
		if c == nil {
			c = &familyCell{}
			cells[s.TransformFamily] = c
		}

		if s.ExpectedMatch {
			if predictedMatch {
				c.tp++
				totalTP++
			} else {
				c.fn++
				totalFN++
			}
		} else {
			if predictedMatch {
				c.fp++
				totalFP++
			} else {
				c.tn++
				totalTN++
			}
		}

		if s.Confidence == string(transform.ConfidenceHigh) {
			if s.ExpectedMatch {
				if predictedMatch {
					c.highTP++
					highTP++
				} else {
					c.highFN++
					highFN++
				}
			} else {
				if predictedMatch {
					c.highFP++
					highFP++
				} else {
					c.highTN++
					highTN++
				}
			}
		} else {
			c.borderlineCount++
			if s.ExpectedMatch {
				if predictedMatch {
					c.borderlineTP++
					borderlineTP++
				} else {
					c.borderlineFN++
					borderlineFN++
				}
			} else {
				if predictedMatch {
					c.borderlineFP++
					borderlineFP++
				} else {
					c.borderlineTN++
					borderlineTN++
				}
			}
		}

		if !correct && c.firstWrongID == "" {
			exp := "match"
			if !s.ExpectedMatch {
				exp = "reject"
			}
			pred := "match"
			if !predictedMatch {
				pred = "no-match"
			}
			c.firstWrongID = s.ID
			c.firstWrongInfo = fmt.Sprintf("confidence=%s want=%s got=%s status=%d certified=%v",
				s.Confidence, exp, pred, res.statusCode, res.certified)
		}
	}

	// --- Phase 3: report ------------------------------------------------------

	allMetrics := buildMetrics(totalTP, totalFP, totalFN, totalTN)
	highMetrics := buildMetrics(highTP, highFP, highFN, highTN)
	borderlineMetrics := buildMetrics(borderlineTP, borderlineFP, borderlineFN, borderlineTN)

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n=== E2E GENERATED DATASET MATRIX ===\n")
	fmt.Fprintf(&sb, "Manifest : %s\n", manifestPath)
	fmt.Fprintf(&sb, "Bases    : %d certified  %d failed\n", len(certifiedHash), certifyFailed)
	fmt.Fprintf(&sb, "Samples  : %d evaluated\n\n", totalTP+totalFP+totalTN+totalFN)

	fams := make([]string, 0, len(cells))
	for f := range cells {
		fams = append(fams, f)
	}
	sort.Strings(fams)

	fmt.Fprintf(&sb, "%-30s  %6s %6s %6s %6s  %8s  %s\n",
		"family", "TP", "FP", "FN", "TN", "pass%", "first-wrong")
	fmt.Fprintln(&sb, strings.Repeat("-", 100))
	for _, fam := range fams {
		c := cells[fam]
		total := c.tp + c.fp + c.fn + c.tn
		passRate := 0.0
		if total > 0 {
			passRate = float64(c.tp+c.tn) / float64(total) * 100
		}
		fw := c.firstWrongInfo
		if fw == "" {
			fw = "—"
		}
		fmt.Fprintf(&sb, "%-30s  %6d %6d %6d %6d  %7.1f%%  %s\n",
			fam, c.tp, c.fp, c.fn, c.tn, passRate, fw)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "All entries      TP=%d FP=%d FN=%d TN=%d  Acc=%.3f Precision=%.3f Recall=%.3f F1=%.3f\n",
		totalTP, totalFP, totalFN, totalTN, allMetrics.Accuracy, allMetrics.Precision, allMetrics.Recall, allMetrics.F1)
	fmt.Fprintf(&sb, "High-CI only     TP=%d FP=%d FN=%d TN=%d  Acc=%.3f Precision=%.3f Recall=%.3f F1=%.3f\n",
		highTP, highFP, highFN, highTN, highMetrics.Accuracy, highMetrics.Precision, highMetrics.Recall, highMetrics.F1)
	fmt.Fprintf(&sb, "Borderline only  TP=%d FP=%d FN=%d TN=%d  Acc=%.3f Precision=%.3f Recall=%.3f F1=%.3f\n",
		borderlineTP, borderlineFP, borderlineFN, borderlineTN, borderlineMetrics.Accuracy, borderlineMetrics.Precision, borderlineMetrics.Recall, borderlineMetrics.F1)
	t.Log(sb.String())

	// Write e2e_report.json alongside manifest.json.
	writeE2EReport(t, filepath.Join(filepath.Dir(manifestPath), "e2e_report.json"),
		cells, fams, totalTP, totalFP, totalFN, totalTN,
		highTP, highFP, highFN, highTN,
		borderlineTP, borderlineFP, borderlineFN, borderlineTN)

	// Gates scale with dataset size (high-confidence only).
	// A 20-image smoke set has high content-variance per sample, so thresholds
	// are intentionally looser than the full 1000-image benchmark.
	// Smoke (<50 bases): loose gates because 20 images have high per-transform
	// variance — content-dependent families (brightness, rotation, crop) vary
	// significantly across images. Full benchmark (≥100 bases) uses strict gates.
	precGate, recGate := 0.85, 0.55
	if m.Metadata.BaseCount >= 100 {
		precGate, recGate = 0.95, 0.75
	}
	if highMetrics.Precision < precGate {
		t.Errorf("precision %.3f < %.2f — too many false positives (high-confidence, %d bases)",
			highMetrics.Precision, precGate, m.Metadata.BaseCount)
	}
	if highMetrics.Recall < recGate {
		t.Errorf("recall %.3f < %.2f — too many false negatives (high-confidence, %d bases)",
			highMetrics.Recall, recGate, m.Metadata.BaseCount)
	}
}

// ---------------------------------------------------------------------------
// report writer
// ---------------------------------------------------------------------------

type familyCell struct {
	tp, fp, tn, fn                 int
	highTP, highFP, highTN, highFN int
	borderlineTP, borderlineFP     int
	borderlineTN, borderlineFN     int
	borderlineCount                int
	firstWrongID                   string
	firstWrongInfo                 string
}

type e2eMetrics struct {
	Total       int     `json:"total"`
	TP          int     `json:"tp"`
	FP          int     `json:"fp"`
	FN          int     `json:"fn"`
	TN          int     `json:"tn"`
	Accuracy    float64 `json:"accuracy"`
	Precision   float64 `json:"precision"`
	Recall      float64 `json:"recall"`
	Specificity float64 `json:"specificity"`
	F1          float64 `json:"f1"`
}

type e2eFamilyReport struct {
	Family         string                `json:"family"`
	TP             int                   `json:"tp"`
	FP             int                   `json:"fp"`
	FN             int                   `json:"fn"`
	TN             int                   `json:"tn"`
	HighTP         int                   `json:"high_tp"`
	HighFP         int                   `json:"high_fp"`
	HighFN         int                   `json:"high_fn"`
	HighTN         int                   `json:"high_tn"`
	BorderlineTP   int                   `json:"borderline_tp"`
	BorderlineFP   int                   `json:"borderline_fp"`
	BorderlineFN   int                   `json:"borderline_fn"`
	BorderlineTN   int                   `json:"borderline_tn"`
	Borderline     int                   `json:"borderline"`
	PassRate       float64               `json:"pass_rate"`
	Metrics        map[string]e2eMetrics `json:"metrics"`
	FirstWrongID   string                `json:"first_wrong_id,omitempty"`
	FirstWrongInfo string                `json:"first_wrong_info,omitempty"`
}

type e2eMatrixReport struct {
	TP           int                   `json:"tp"`
	FP           int                   `json:"fp"`
	FN           int                   `json:"fn"`
	TN           int                   `json:"tn"`
	HighTP       int                   `json:"high_tp"`
	HighFP       int                   `json:"high_fp"`
	HighFN       int                   `json:"high_fn"`
	HighTN       int                   `json:"high_tn"`
	BorderlineTP int                   `json:"borderline_tp"`
	BorderlineFP int                   `json:"borderline_fp"`
	BorderlineFN int                   `json:"borderline_fn"`
	BorderlineTN int                   `json:"borderline_tn"`
	Accuracy     float64               `json:"accuracy"`
	Precision    float64               `json:"precision"`
	Recall       float64               `json:"recall"`
	Specificity  float64               `json:"specificity"`
	F1           float64               `json:"f1"`
	Metrics      map[string]e2eMetrics `json:"metrics"`
	Families     []e2eFamilyReport     `json:"families"`
}

func writeE2EReport(t *testing.T, path string,
	cells map[string]*familyCell, fams []string,
	tp, fp, fn, tn, htp, hfp, hfn, htn int,
	btp, bfp, bfn, btn int,
) {
	t.Helper()
	var famReports []e2eFamilyReport
	for _, fam := range fams {
		c := cells[fam]
		total := c.tp + c.fp + c.fn + c.tn
		rate := 0.0
		if total > 0 {
			rate = float64(c.tp+c.tn) / float64(total)
		}
		all := buildMetrics(c.tp, c.fp, c.fn, c.tn)
		high := buildMetrics(c.highTP, c.highFP, c.highFN, c.highTN)
		borderline := buildMetrics(c.borderlineTP, c.borderlineFP, c.borderlineFN, c.borderlineTN)
		famReports = append(famReports, e2eFamilyReport{
			Family: fam, TP: c.tp, FP: c.fp, FN: c.fn, TN: c.tn,
			HighTP: c.highTP, HighFP: c.highFP, HighFN: c.highFN, HighTN: c.highTN,
			BorderlineTP: c.borderlineTP, BorderlineFP: c.borderlineFP,
			BorderlineFN: c.borderlineFN, BorderlineTN: c.borderlineTN,
			Borderline: c.borderlineCount, PassRate: rate,
			Metrics: map[string]e2eMetrics{
				"all":        all,
				"high":       high,
				"borderline": borderline,
			},
			FirstWrongID: c.firstWrongID, FirstWrongInfo: c.firstWrongInfo,
		})
	}
	all := buildMetrics(tp, fp, fn, tn)
	high := buildMetrics(htp, hfp, hfn, htn)
	borderline := buildMetrics(btp, bfp, bfn, btn)
	r := e2eMatrixReport{
		TP: tp, FP: fp, FN: fn, TN: tn,
		HighTP: htp, HighFP: hfp, HighFN: hfn, HighTN: htn,
		BorderlineTP: btp, BorderlineFP: bfp, BorderlineFN: bfn, BorderlineTN: btn,
		Accuracy: all.Accuracy, Precision: all.Precision, Recall: all.Recall,
		Specificity: all.Specificity, F1: all.F1,
		Metrics: map[string]e2eMetrics{
			"all":        all,
			"high":       high,
			"borderline": borderline,
		},
		Families: famReports,
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Logf("marshal e2e_report: %v", err)
		return
	}
	if werr := os.WriteFile(path, b, 0644); werr != nil {
		t.Logf("write e2e_report: %v", werr)
		return
	}
	t.Logf("e2e report written to %s", path)
}

func safeRatio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func buildMetrics(tp, fp, fn, tn int) e2eMetrics {
	total := tp + fp + fn + tn
	precision := safeRatio(tp, tp+fp)
	recall := safeRatio(tp, tp+fn)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return e2eMetrics{
		Total:       total,
		TP:          tp,
		FP:          fp,
		FN:          fn,
		TN:          tn,
		Accuracy:    safeRatio(tp+tn, total),
		Precision:   precision,
		Recall:      recall,
		Specificity: safeRatio(tn, tn+fp),
		F1:          f1,
	}
}
