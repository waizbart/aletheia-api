//go:build integration

package feature_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/dataset/manifest"
	"github.com/waizbart/aletheia-api/internal/dataset/transform"
	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/testdata"
)

// TestDataset_FullMatrix runs the full transform taxonomy against either:
//  1. A generated manifest (ALETHEIA_DATASET_MANIFEST or testdata/generated/manifest.json)
//  2. The in-memory smoke-base fallback (always available, no network needed)
//
// It gates on precision ≥ 0.95 and recall ≥ 0.75 for high-confidence entries
// and writes a per-family confusion matrix to testdata/generated/report.json.
func TestDataset_FullMatrix(t *testing.T) {
	manifestPath := testdata.ManifestPath()
	if _, err := os.Stat(manifestPath); err == nil {
		t.Logf("running manifest-driven eval from %s", manifestPath)
		runManifestEval(t, manifestPath)
	} else {
		t.Logf("no manifest found at %s, running smoke-base in-memory eval", manifestPath)
		runSmokeEval(t)
	}
}

// ---------------------------------------------------------------------------
// Manifest-driven mode (generated dataset)
// ---------------------------------------------------------------------------

func runManifestEval(t *testing.T, manifestPath string) {
	t.Helper()
	m, err := manifest.ReadJSON(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	// Load all unique bases referenced in the manifest.
	type baseEntry struct {
		bytes    []byte
		sig      *domain.FeatureSignature
		refImage []byte
	}
	bases := make(map[string]baseEntry)
	for _, s := range m.Samples {
		if _, ok := bases[s.BaseImageID]; ok {
			continue
		}
		b, err := os.ReadFile(s.SourcePath)
		if err != nil {
			t.Logf("skip base %s: %v", s.BaseImageID, err)
			continue
		}
		sig, refImage, err := ext.Compute(ctx, b)
		if err != nil {
			t.Logf("skip base %s: compute: %v", s.BaseImageID, err)
			continue
		}
		bases[s.BaseImageID] = baseEntry{bytes: b, sig: sig, refImage: refImage}
	}
	t.Logf("loaded %d unique bases", len(bases))

	cells := make(map[string]*manifestCell)
	var tp, fp, tn, fn int
	var tpH, fpH, tnH, fnH int

	for _, s := range m.Samples {
		base, ok := bases[s.BaseImageID]
		if !ok {
			continue
		}
		varB, err := os.ReadFile(s.OutputPath)
		if err != nil {
			t.Logf("skip %s: read variant: %v", s.ID, err)
			continue
		}
		candSig, _, cerr := ext.Compute(ctx, varB)
		if cerr != nil {
			continue
		}
		dec, merr := ext.Match(ctx, base.sig, candSig, base.refImage, varB)
		if merr != nil {
			continue
		}
		matched := dec.Matched
		correct := matched == s.ExpectedMatch

		c := cells[s.TransformFamily]
		if c == nil {
			c = &manifestCell{family: s.TransformFamily}
			cells[s.TransformFamily] = c
		}
		if correct {
			c.pass++
		} else {
			c.fail++
		}
		if s.Confidence == string(transform.ConfidenceHigh) {
			if correct {
				c.highPass++
			} else {
				c.highFail++
			}
		} else {
			c.borderlineLog = append(c.borderlineLog,
				fmt.Sprintf("%s matched=%v want=%v inliers=%d mean=%.2f max=%.2f",
					s.ID, matched, s.ExpectedMatch, dec.Inliers, dec.ColorMean, dec.ColorMax))
		}

		if s.ExpectedMatch {
			if matched {
				tp++
			} else {
				fn++
			}
		} else {
			if matched {
				fp++
			} else {
				tn++
			}
		}
		if s.Confidence == string(transform.ConfidenceHigh) {
			if s.ExpectedMatch {
				if matched {
					tpH++
				} else {
					fnH++
				}
			} else {
				if matched {
					fpH++
				} else {
					tnH++
				}
			}
		}
	}

	precision := safeDiv(tpH, tpH+fpH)
	recall := safeDiv(tpH, tpH+fnH)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}

	// Write report.json
	reportPath := testdata.Generated("report.json")
	writeReport(t, reportPath, cells, tp, fp, tn, fn, tpH, fpH, tnH, fnH, precision, recall, f1)

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n=== MANIFEST EVAL REPORT ===\n")
	fmt.Fprintf(&sb, "Manifest: %s\n", manifestPath)
	fmt.Fprintf(&sb, "All    : TP=%d FP=%d FN=%d TN=%d\n", tp, fp, fn, tn)
	fmt.Fprintf(&sb, "High-CI: TP=%d FP=%d FN=%d TN=%d  Precision=%.3f Recall=%.3f F1=%.3f\n",
		tpH, fpH, fnH, tnH, precision, recall, f1)
	fmt.Fprintln(&sb, "\nPer-family (high-confidence):")
	fams := make([]string, 0, len(cells))
	for f := range cells {
		fams = append(fams, f)
	}
	sort.Strings(fams)
	for _, fam := range fams {
		c := cells[fam]
		fmt.Fprintf(&sb, "  %-30s pass=%d/%d (high: pass=%d/%d)\n",
			fam, c.pass, c.pass+c.fail, c.highPass, c.highPass+c.highFail)
	}
	t.Log(sb.String())

	if precision < 0.95 {
		t.Errorf("precision %.3f below 0.95 — false-positive rate too high", precision)
	}
	if recall < 0.75 {
		t.Errorf("recall %.3f below 0.75 — false-negative rate too high", recall)
	}
}

// ---------------------------------------------------------------------------
// Smoke-base in-memory mode (no generated manifest needed)
// ---------------------------------------------------------------------------

func runSmokeEval(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	smokeDir := testdata.Curated("smoke-base")
	files, err := listBaseImages(t, smokeDir)
	if err != nil {
		t.Fatalf("list base: %v", err)
	}
	if len(files) < 20 {
		t.Fatalf("need >=20 base images in testdata/curated/smoke-base, got %d", len(files))
	}

	type baseEntry struct {
		name     string
		bytes    []byte
		sig      *domain.FeatureSignature
		refImage []byte
	}
	bases := make([]baseEntry, 0, len(files))
	for _, fp := range files {
		b, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("read %s: %v", fp, err)
		}
		sig, refImage, err := ext.Compute(ctx, b)
		if err != nil {
			t.Logf("skip %s: extractor rejected base (%v)", filepath.Base(fp), err)
			continue
		}
		bases = append(bases, baseEntry{
			name:     filepath.Base(fp),
			bytes:    b,
			sig:      sig,
			refImage: refImage,
		})
	}
	if len(bases) < 20 {
		t.Fatalf("only %d/%d bases produced features", len(bases), len(files))
	}

	entries := transform.Registry()

	type cell struct {
		entry         transform.Entry
		pass          int
		fail          int
		firstFailBase string
		firstFailDec  string
	}
	cells := make(map[string]*cell, len(entries))
	for _, e := range entries {
		cells[e.Name] = &cell{entry: e}
	}

	var tp, fp, tn, fn int
	var tpH, fpH, tnH, fnH int

	for i, base := range bases {
		peer := bases[(i+1)%len(bases)]
		for _, e := range entries {
			builder, berr := transform.BuilderFor(e)
			if berr != nil {
				continue
			}
			candBytes, buildErr := builder(base.bytes, peer.bytes)
			c := cells[e.Name]

			matched := false
			var detail string
			if buildErr != nil {
				detail = fmt.Sprintf("build err: %v", buildErr)
			} else {
				candSig, _, cerr := ext.Compute(ctx, candBytes)
				if cerr != nil {
					detail = fmt.Sprintf("extract err: %v", cerr)
				} else {
					dec, merr := ext.Match(ctx, base.sig, candSig, base.refImage, candBytes)
					if merr != nil {
						detail = fmt.Sprintf("match err: %v", merr)
					} else {
						matched = dec.Matched
						detail = fmt.Sprintf("inliers=%d mean=%.2f max=%.2f cells=%d",
							dec.Inliers, dec.ColorMean, dec.ColorMax, dec.Cells)
					}
				}
			}

			correct := matched == e.ExpectedMatch
			if correct {
				c.pass++
			} else {
				c.fail++
				if c.firstFailBase == "" {
					c.firstFailBase = base.name
					c.firstFailDec = detail
				}
			}
			if e.ExpectedMatch {
				if matched {
					tp++
				} else {
					fn++
				}
			} else {
				if matched {
					fp++
				} else {
					tn++
				}
			}
			if e.Confidence == transform.ConfidenceHigh {
				if e.ExpectedMatch {
					if matched {
						tpH++
					} else {
						fnH++
					}
				} else {
					if matched {
						fpH++
					} else {
						tnH++
					}
				}
			}
		}
	}

	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n=== SMOKE-BASE MATRIX REPORT ===\n")
	fmt.Fprintf(&sb, "Bases: %d   Variants/base: %d\n\n", len(bases), len(entries))
	fmt.Fprintf(&sb, "%-32s %-9s %-12s %-7s %s\n", "variant", "expects", "confidence", "pass", "first-fail")
	fmt.Fprintln(&sb, strings.Repeat("-", 100))
	for _, n := range names {
		c := cells[n]
		exp := "match"
		if !c.entry.ExpectedMatch {
			exp = "REJECT"
		}
		ratio := fmt.Sprintf("%d/%d", c.pass, c.pass+c.fail)
		first := ""
		if c.fail > 0 {
			first = c.firstFailBase + " " + c.firstFailDec
		}
		fmt.Fprintf(&sb, "%-32s %-9s %-12s %-7s %s\n", c.entry.Name, exp, c.entry.Confidence, ratio, first)
	}
	fmt.Fprintln(&sb)
	precision := safeDiv(tpH, tpH+fpH)
	recall := safeDiv(tpH, tpH+fnH)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	fmt.Fprintf(&sb, "All    : TP=%d FP=%d FN=%d TN=%d\n", tp, fp, fn, tn)
	fmt.Fprintf(&sb, "High-CI: TP=%d FP=%d FN=%d TN=%d  Precision=%.3f Recall=%.3f F1=%.3f\n",
		tpH, fpH, fnH, tnH, precision, recall, f1)
	t.Log(sb.String())

	if precision < 0.95 {
		t.Errorf("precision %.3f below 0.95 — false-positive rate too high (high-confidence entries)", precision)
	}
	if recall < 0.75 {
		t.Errorf("recall %.3f below 0.75 — false-negative rate too high (high-confidence entries)", recall)
	}
}

// ---------------------------------------------------------------------------
// report.json writer
// ---------------------------------------------------------------------------

type familyReport struct {
	Family       string  `json:"family"`
	Pass         int     `json:"pass"`
	Fail         int     `json:"fail"`
	HighPass     int     `json:"high_pass"`
	HighFail     int     `json:"high_fail"`
	HighPrecRate float64 `json:"high_pass_rate"`
}

type matrixReport struct {
	TP        int            `json:"tp"`
	FP        int            `json:"fp"`
	FN        int            `json:"fn"`
	TN        int            `json:"tn"`
	HighTP    int            `json:"high_tp"`
	HighFP    int            `json:"high_fp"`
	HighFN    int            `json:"high_fn"`
	HighTN    int            `json:"high_tn"`
	Precision float64        `json:"precision"`
	Recall    float64        `json:"recall"`
	F1        float64        `json:"f1"`
	Families  []familyReport `json:"families"`
}

// manifestCell accumulates per-family pass/fail tallies for the manifest eval.
type manifestCell struct {
	family        string
	pass          int
	fail          int
	highPass      int
	highFail      int
	borderlineLog []string
}

func writeReport(t *testing.T,
	path string,
	cells map[string]*manifestCell,
	tp, fp, tn, fn, tpH, fpH, tnH, fnH int,
	precision, recall, f1 float64,
) {
	t.Helper()

	fams := make([]string, 0, len(cells))
	for f := range cells {
		fams = append(fams, f)
	}
	sort.Strings(fams)

	var famReports []familyReport
	for _, fam := range fams {
		c := cells[fam]
		total := c.highPass + c.highFail
		rate := 0.0
		if total > 0 {
			rate = float64(c.highPass) / float64(total)
		}
		famReports = append(famReports, familyReport{
			Family:       fam,
			Pass:         c.pass,
			Fail:         c.fail,
			HighPass:     c.highPass,
			HighFail:     c.highFail,
			HighPrecRate: rate,
		})
	}

	r := matrixReport{
		TP: tp, FP: fp, FN: fn, TN: tn,
		HighTP: tpH, HighFP: fpH, HighFN: fnH, HighTN: tnH,
		Precision: precision, Recall: recall, F1: f1,
		Families: famReports,
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Logf("mkdir for report: %v", err)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Logf("marshal report: %v", err)
		return
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Logf("write report: %v", err)
		return
	}
	t.Logf("report written to %s", path)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func listBaseImages(t *testing.T, dir string) ([]string, error) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".png") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func safeDiv(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
