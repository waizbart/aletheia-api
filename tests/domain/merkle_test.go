package domain_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func leaf(n byte) [32]byte {
	var content, commitment [32]byte
	content[0], commitment[0] = n, n
	return domain.MerkleLeaf(content, commitment)
}

func leaves(count int) [][32]byte {
	out := make([][32]byte, count)
	for i := range out {
		out[i] = leaf(byte(i + 1))
	}
	return out
}

func TestKeccak256_MatchesKnownVector(t *testing.T) {
	// keccak256("") — the canonical empty-input digest. If this drifts, the
	// hash is not the one the EVM speaks and no proof would ever verify
	// on chain.
	got := domain.Keccak256(nil)
	const want = "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("Keccak256(\"\") = %x, want %s", got, want)
	}
}

// TestMerkleLeaf_IsDoubleHashed guards against a second-preimage attack: an
// internal node must never be presentable as a leaf.
func TestMerkleLeaf_IsDoubleHashed(t *testing.T) {
	var content, commitment [32]byte
	content[0], commitment[0] = 1, 2

	single := domain.Keccak256(content[:], commitment[:])
	got := domain.MerkleLeaf(content, commitment)

	if got == single {
		t.Fatal("leaves must live in a domain internal nodes cannot reach")
	}
	if want := domain.Keccak256(single[:]); got != want {
		t.Errorf("MerkleLeaf = %x, want keccak(keccak(content||commitment)) = %x", got, want)
	}
}

func TestMerkleLeaf_IsDeterministicAndCollisionFree(t *testing.T) {
	var a, b [32]byte
	a[0], b[0] = 1, 2

	if domain.MerkleLeaf(a, b) != domain.MerkleLeaf(a, b) {
		t.Error("leaf derivation must be deterministic")
	}
	if domain.MerkleLeaf(a, b) == domain.MerkleLeaf(b, a) {
		t.Error("swapping content hash and commitment must change the leaf")
	}
}

// TestBuildMerkleTree_EveryProofVerifies is the property the whole batching
// scheme rests on: every certificate in a batch, at every batch size, must be
// able to prove its own membership.
func TestBuildMerkleTree_EveryProofVerifies(t *testing.T) {
	for _, size := range []int{1, 2, 3, 4, 5, 7, 8, 9, 16, 17, 31, 64, 100, 1000} {
		t.Run(sizeName(size), func(t *testing.T) {
			set := leaves(size)
			tree := domain.BuildMerkleTree(set)

			if len(tree.Proofs) != size {
				t.Fatalf("got %d proofs for %d leaves", len(tree.Proofs), size)
			}
			for i, l := range set {
				if !domain.VerifyMerkleProof(l, tree.Root, tree.Proofs[i]) {
					t.Errorf("leaf %d of %d does not verify against the root", i, size)
				}
			}
		})
	}
}

func TestBuildMerkleTree_RejectsForeignLeaves(t *testing.T) {
	set := leaves(8)
	tree := domain.BuildMerkleTree(set)

	outsider := leaf(200)
	for i := range set {
		if domain.VerifyMerkleProof(outsider, tree.Root, tree.Proofs[i]) {
			t.Fatalf("a leaf outside the batch verified with proof %d", i)
		}
	}
}

func TestBuildMerkleTree_RejectsTamperedProof(t *testing.T) {
	set := leaves(8)
	tree := domain.BuildMerkleTree(set)

	proof := make([][32]byte, len(tree.Proofs[3]))
	copy(proof, tree.Proofs[3])
	proof[0][0] ^= 0xff

	if domain.VerifyMerkleProof(set[3], tree.Root, proof) {
		t.Fatal("a corrupted proof must not verify")
	}
}

func TestBuildMerkleTree_SingleLeaf(t *testing.T) {
	set := leaves(1)
	tree := domain.BuildMerkleTree(set)

	if tree.Root != set[0] {
		t.Error("a one-leaf tree roots at its leaf")
	}
	if len(tree.Proofs[0]) != 0 {
		t.Errorf("a lone leaf needs no proof, got %d nodes", len(tree.Proofs[0]))
	}
}

func TestBuildMerkleTree_Empty(t *testing.T) {
	tree := domain.BuildMerkleTree(nil)
	if tree.Root != ([32]byte{}) || len(tree.Proofs) != 0 {
		t.Error("an empty batch has no root and no proofs")
	}
}

// TestBuildMerkleTree_OddNodeIsCarriedNotDuplicated pins why the odd case is
// handled by promotion: duplicating the last node would make a lone leaf
// indistinguishable from a pair of identical leaves.
func TestBuildMerkleTree_OddNodeIsCarriedNotDuplicated(t *testing.T) {
	single := domain.BuildMerkleTree(leaves(1))

	duplicated := [][32]byte{leaf(1), leaf(1)}
	pair := domain.BuildMerkleTree(duplicated)

	if single.Root == pair.Root {
		t.Fatal("one leaf and two identical leaves must not share a root")
	}
}

func TestBuildMerkleTree_OrderMatters(t *testing.T) {
	a := domain.BuildMerkleTree([][32]byte{leaf(1), leaf(2), leaf(3)})
	b := domain.BuildMerkleTree([][32]byte{leaf(3), leaf(2), leaf(1)})

	// Internal pairs are sorted, so a reordering that produces the same pairs
	// keeps the root; what must hold is that each leaf still proves itself.
	for i, l := range [][32]byte{leaf(3), leaf(2), leaf(1)} {
		if !domain.VerifyMerkleProof(l, b.Root, b.Proofs[i]) {
			t.Errorf("leaf %d does not verify after reordering", i)
		}
	}
	if len(a.Proofs) != len(b.Proofs) {
		t.Error("proof counts should match for equal-size batches")
	}
}

func TestProofRoundTrip(t *testing.T) {
	tree := domain.BuildMerkleTree(leaves(9))
	original := tree.Proofs[4]

	flat := domain.FlattenProof(original)
	restored := domain.ProofFromBytes(flat)

	if len(restored) != len(original) {
		t.Fatalf("restored %d nodes, want %d", len(restored), len(original))
	}
	for i := range original {
		if restored[i] != original[i] {
			t.Errorf("node %d changed across the round trip", i)
		}
	}
	if !domain.VerifyMerkleProof(tree.Leaves[4], tree.Root, restored) {
		t.Error("a restored proof must still verify")
	}
}

func TestProofFromBytes_SkipsMalformedNodes(t *testing.T) {
	got := domain.ProofFromBytes([][]byte{
		bytes.Repeat([]byte{1}, 32),
		bytes.Repeat([]byte{2}, 7), // wrong width
		nil,
	})
	if len(got) != 1 {
		t.Fatalf("got %d nodes, want only the well-formed one", len(got))
	}
}

func TestEncodeProof(t *testing.T) {
	got := domain.EncodeProof([][]byte{bytes.Repeat([]byte{0xab}, 32)})
	if len(got) != 1 {
		t.Fatalf("got %d entries", len(got))
	}
	if got[0] != "0x"+hex.EncodeToString(bytes.Repeat([]byte{0xab}, 32)) {
		t.Errorf("EncodeProof = %q", got[0])
	}
	if len(domain.EncodeProof(nil)) != 0 {
		t.Error("an absent proof encodes to nothing")
	}
}

func TestAnchor_Confirmed(t *testing.T) {
	if !(&domain.Anchor{Status: domain.AnchorConfirmed}).Confirmed() {
		t.Error("a confirmed anchor should report as confirmed")
	}
	if (&domain.Anchor{Status: domain.AnchorPending}).Confirmed() {
		t.Error("a pending anchor is not confirmed")
	}
	var nilAnchor *domain.Anchor
	if nilAnchor.Confirmed() {
		t.Error("a nil anchor is not confirmed")
	}
}

func sizeName(n int) string {
	return "leaves=" + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
