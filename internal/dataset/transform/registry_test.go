package transform_test

import (
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/dataset/transform"
)

func TestRegistry_NoDuplicateNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range transform.Registry() {
		if seen[e.Name] {
			t.Errorf("duplicate entry name: %q", e.Name)
		}
		seen[e.Name] = true
	}
}

func TestRegistry_RequiredFields(t *testing.T) {
	for _, e := range transform.Registry() {
		if e.Name == "" {
			t.Errorf("entry has empty Name: %+v", e)
		}
		if e.Family == "" {
			t.Errorf("entry %q has empty Family", e.Name)
		}
		if e.MIMEType == "" {
			t.Errorf("entry %q has empty MIMEType", e.Name)
		}
		if e.Rationale == "" {
			t.Errorf("entry %q has empty Rationale", e.Name)
		}
		if e.Confidence != transform.ConfidenceHigh && e.Confidence != transform.ConfidenceBorderline {
			t.Errorf("entry %q has invalid confidence %q", e.Name, e.Confidence)
		}
	}
}

func TestRegistry_NegativeControlExists(t *testing.T) {
	found := false
	for _, e := range transform.Registry() {
		if e.Family == "different_image" {
			found = true
			if e.ExpectedMatch {
				t.Errorf("different_image entry must have ExpectedMatch=false")
			}
		}
	}
	if !found {
		t.Error("registry must contain at least one different_image entry")
	}
}

func TestRegistry_HighConfidenceSubset(t *testing.T) {
	all := transform.Registry()
	high := transform.HighConfidence()
	if len(high) >= len(all) {
		t.Errorf("HighConfidence() returned all %d entries; borderline entries missing", len(all))
	}
	for _, e := range high {
		if e.Confidence != transform.ConfidenceHigh {
			t.Errorf("HighConfidence() returned borderline entry %q", e.Name)
		}
	}
}

func TestRegistry_ByFamily(t *testing.T) {
	byFam := transform.ByFamily()
	if len(byFam) == 0 {
		t.Fatal("ByFamily() returned empty map")
	}
	for fam, entries := range byFam {
		for _, e := range entries {
			if e.Family != fam {
				t.Errorf("ByFamily[%q] contains entry with Family=%q", fam, e.Family)
			}
		}
	}
}

func TestRegistry_NameContainsFamilyRoot(t *testing.T) {
	for _, e := range transform.Registry() {
		// Name should contain the first word of the family (e.g. "rotate" for "rotate_cardinal").
		familyRoot := strings.SplitN(e.Family, "_", 2)[0]
		if !strings.Contains(e.Name, familyRoot) {
			t.Errorf("entry name %q does not contain family root %q (family=%q)", e.Name, familyRoot, e.Family)
		}
	}
}
