package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestFeatureCommitment_DeterministicForEmptyBundle(t *testing.T) {
	a := domain.FeatureCommitment(nil, nil)
	b := domain.FeatureCommitment(nil, nil)
	if a != b {
		t.Fatalf("empty commitment is not deterministic: %x vs %x", a, b)
	}
	if a == ([32]byte{}) {
		t.Fatal("empty commitment must not be the zero value")
	}
}

func TestFeatureCommitment_ChangesWithPHash(t *testing.T) {
	var p1, p2 [32]byte
	p1[0] = 0x01
	p2[0] = 0x02
	if domain.FeatureCommitment(&p1, nil) == domain.FeatureCommitment(&p2, nil) {
		t.Fatal("commitment did not change when pHash changed")
	}
}

func TestFeatureCommitment_ChangesWithSignature(t *testing.T) {
	var p [32]byte
	sig1 := &domain.FeatureSignature{Descriptors: []byte("a"), Keypoints: []byte("k")}
	sig2 := &domain.FeatureSignature{Descriptors: []byte("b"), Keypoints: []byte("k")}
	if domain.FeatureCommitment(&p, sig1) == domain.FeatureCommitment(&p, sig2) {
		t.Fatal("commitment did not change when descriptors changed")
	}
	sig3 := &domain.FeatureSignature{Descriptors: []byte("a"), Keypoints: []byte("kk")}
	if domain.FeatureCommitment(&p, sig1) == domain.FeatureCommitment(&p, sig3) {
		t.Fatal("commitment did not change when keypoints changed")
	}
}

func TestFeatureCommitment_LayoutIsSha256OfConcatenatedHashes(t *testing.T) {
	var phash [32]byte
	for i := range phash {
		phash[i] = byte(i)
	}
	sig := &domain.FeatureSignature{
		Descriptors: []byte{0xaa, 0xbb},
		Keypoints:   []byte{0xcc},
	}

	descHash := sha256.Sum256(sig.Descriptors)
	kpHash := sha256.Sum256(sig.Keypoints)
	expectedInput := append(append(append([]byte{}, phash[:]...), descHash[:]...), kpHash[:]...)
	expected := sha256.Sum256(expectedInput)

	got := domain.FeatureCommitment(&phash, sig)
	if got != expected {
		t.Fatalf("commitment layout mismatch:\n got = %x\nwant = %x", got, expected)
	}
}

func TestFeatureCommitmentHex_MatchesBytes(t *testing.T) {
	var phash [32]byte
	phash[0] = 0xff
	c := domain.FeatureCommitment(&phash, nil)
	got := domain.FeatureCommitmentHex(&phash, nil)
	if got != hex.EncodeToString(c[:]) {
		t.Fatalf("hex mismatch: %s vs %x", got, c)
	}
	if len(got) != 64 {
		t.Fatalf("hex length = %d, want 64", len(got))
	}
}
