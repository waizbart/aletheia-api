package domain_test

import (
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestPHashBands_LengthAndIdentityForBytewiseHash(t *testing.T) {
	if domain.PHashBandCount != 32 {
		t.Fatalf("unexpected PHashBandCount: %d", domain.PHashBandCount)
	}
	var phash [32]byte
	for i := range phash {
		phash[i] = byte(i * 7)
	}
	bands := domain.PHashBands(phash)
	for i := 0; i < domain.PHashBandCount; i++ {
		if bands[i] != phash[i] {
			t.Fatalf("band %d = %d, want %d", i, bands[i], phash[i])
		}
	}
}

func TestPHashBands_DifferByOneBitAffectsExactlyOneBand(t *testing.T) {
	var a [32]byte
	var b [32]byte
	b[5] = 0x80

	ba := domain.PHashBands(a)
	bb := domain.PHashBands(b)

	diffs := 0
	for i := 0; i < domain.PHashBandCount; i++ {
		if ba[i] != bb[i] {
			diffs++
		}
	}
	if diffs != 1 {
		t.Fatalf("expected exactly one band to differ, got %d", diffs)
	}
}
