//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/domain"
	dbmigrate "github.com/waizbart/aletheia-api/internal/migrate"
	"github.com/waizbart/aletheia-api/internal/repository"
)

// TestPostgresVector_FindCandidatesByPHashes exercises the pgvector bit(256)
// Hamming pre-filter against a real Postgres. Skipped when DATABASE_URL is not
// set so that `go test ./...` keeps working without docker-compose.
//
// Migrations are applied via the golang-migrate runner at start so the test is
// self-sufficient even on a fresh database. Requires the pgvector/pgvector image.
func TestPostgresVector_FindCandidatesByPHashes(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping postgres integration test (start docker-compose to enable)")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Skipf("cannot reach postgres: %v", err)
	}

	if err := dbmigrate.Run(dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	if _, err := db.ExecContext(ctx, "TRUNCATE certificates CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	repo := repository.NewPostgresCertificateRepo(db)

	// Reference pHash with a recognizable byte pattern so we can derive
	// neighbours by flipping known bits.
	var refPHash [32]byte
	for i := range refPHash {
		refPHash[i] = byte(0xA0 | (i & 0x0F))
	}

	refCert := &domain.Certificate{
		ContentHash: "ref-content-hash-" + uniqueSuffix(t),
		PHash:       &refPHash,
		Signature: &domain.FeatureSignature{
			Descriptors: []byte{0xDE, 0xAD},
			Keypoints:   []byte{0xBE, 0xEF},
		},
		Registrant:  "tester",
		TxHash:      "0xref",
		BlockNumber: 1,
		CreatedAt:   time.Now().UTC(),
	}
	commit := domain.FeatureCommitment(refCert.PHash, refCert.Signature)
	refCert.FeatureCommitment = &commit

	if err := repo.Save(ctx, refCert); err != nil {
		t.Fatalf("save ref cert: %v", err)
	}

	// Cert with a different pattern in every byte — no band can collide.
	var farPHash [32]byte
	for i := range farPHash {
		farPHash[i] = byte(0x10 + i)
	}
	farCert := &domain.Certificate{
		ContentHash: "far-content-hash-" + uniqueSuffix(t),
		PHash:       &farPHash,
		Signature:   &domain.FeatureSignature{Descriptors: []byte{0x01}, Keypoints: []byte{0x02}},
		Registrant:  "tester",
		TxHash:      "0xfar",
		BlockNumber: 2,
		CreatedAt:   time.Now().UTC(),
	}
	if err := repo.Save(ctx, farCert); err != nil {
		t.Fatalf("save far cert: %v", err)
	}

	// Cert without any pHash (non-image content) must not appear in
	// candidate results, but also not break the repository.
	noHashCert := &domain.Certificate{
		ContentHash: "no-phash-content-" + uniqueSuffix(t),
		Registrant:  "tester",
		TxHash:      "0xnone",
		BlockNumber: 3,
		CreatedAt:   time.Now().UTC(),
	}
	if err := repo.Save(ctx, noHashCert); err != nil {
		t.Fatalf("save no-phash cert: %v", err)
	}

	t.Run("phash_bits is a populated 256-bit vector for cert with phash", func(t *testing.T) {
		var length int
		if err := db.QueryRowContext(ctx,
			"SELECT length(phash_bits) FROM certificates WHERE id = $1::uuid", refCert.ID,
		).Scan(&length); err != nil {
			t.Fatalf("read phash_bits length: %v", err)
		}
		if length != 256 {
			t.Fatalf("expected phash_bits to be 256 bits, got %d", length)
		}
	})

	t.Run("phash_bits is NULL for cert without phash", func(t *testing.T) {
		var isNull bool
		if err := db.QueryRowContext(ctx,
			"SELECT phash_bits IS NULL FROM certificates WHERE id = $1::uuid", noHashCert.ID,
		).Scan(&isNull); err != nil {
			t.Fatalf("read phash_bits null: %v", err)
		}
		if !isNull {
			t.Fatal("expected phash_bits to be NULL for non-image cert")
		}
	})

	t.Run("exact phash returns the cert at distance 0", func(t *testing.T) {
		hits, err := repo.FindCandidatesByPHashes(ctx, [][32]byte{refPHash}, domain.MaxPHashDistance, 10)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if len(hits) == 0 {
			t.Fatal("expected at least one hit, got none")
		}
		if hits[0].ID != refCert.ID {
			t.Fatalf("expected ref cert as best match, got id=%s", hits[0].ID)
		}
	})

	t.Run("single byte flip still recovered via the unaffected bands", func(t *testing.T) {
		nearPHash := refPHash
		nearPHash[5] ^= 0xFF // 8 bits flipped, but only band 5 differs.
		hits, err := repo.FindCandidatesByPHashes(ctx, [][32]byte{nearPHash}, domain.MaxPHashDistance, 10)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if !containsCert(hits, refCert.ID) {
			t.Fatalf("expected ref cert in hits when only band 5 differs; got %d hits", len(hits))
		}
	})

	t.Run("rotations: min hamming over candidate variants is used", func(t *testing.T) {
		// First variant intentionally far (no band collision); the second
		// variant matches exactly. The repository must use the minimum.
		hits, err := repo.FindCandidatesByPHashes(ctx, [][32]byte{farPHashUnrelated(), refPHash}, domain.MaxPHashDistance, 10)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if !containsCert(hits, refCert.ID) {
			t.Fatal("expected ref cert when one of the rotations matches exactly")
		}
	})

	t.Run("maxDistance filters out too-far cert that still collided on a band", func(t *testing.T) {
		// Build a phash that shares band 0 with refPHash but differs in many
		// other bytes — collides on a band so it enters the candidate set,
		// but full Hamming exceeds the small maxDistance we pass.
		probe := refPHash
		for i := 1; i < 32; i++ {
			probe[i] ^= 0xFF // flips 8*31 = 248 bits
		}
		hits, err := repo.FindCandidatesByPHashes(ctx, [][32]byte{probe}, 10, 10)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if containsCert(hits, refCert.ID) {
			t.Fatal("ref cert should be filtered out by maxDistance=10 when actual distance is large")
		}
	})

	t.Run("topK limits the result count", func(t *testing.T) {
		// Both refCert and farCert exist; query both phashes so each is its
		// own exact match. topK=1 must return only the closest.
		hits, err := repo.FindCandidatesByPHashes(ctx, [][32]byte{refPHash, farPHash}, domain.MaxPHashDistance, 1)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("topK=1 should return 1 cert, got %d", len(hits))
		}
	})

	t.Run("FindByHash hydrates feature commitment", func(t *testing.T) {
		got, err := repo.FindByHash(ctx, refCert.ContentHash)
		if err != nil {
			t.Fatalf("find by hash: %v", err)
		}
		if got == nil {
			t.Fatal("expected cert, got nil")
		}
		if got.FeatureCommitment == nil {
			t.Fatal("expected feature commitment to be hydrated")
		}
		if *got.FeatureCommitment != commit {
			t.Fatalf("commitment mismatch: got %x want %x", *got.FeatureCommitment, commit)
		}
	})
}

func containsCert(hits []*domain.Certificate, id string) bool {
	for _, h := range hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

func farPHashUnrelated() [32]byte {
	var p [32]byte
	for i := range p {
		p[i] = byte(0x55) // 0x55 vs 0xA0|(i&0x0F) shares no bytes with refPHash.
	}
	return p
}

func uniqueSuffix(t *testing.T) string {
	t.Helper()
	return time.Now().Format("20060102150405.000000000") + "-" + t.Name()
}
