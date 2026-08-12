package domain

import (
	"bytes"
	"encoding/hex"

	"golang.org/x/crypto/sha3"
)

// Merkle batching is what makes anchoring affordable. One transaction per
// certificate costs a transaction per certificate; one transaction per batch
// costs the same regardless of how many certificates it commits, and every
// certificate still gets an independently checkable proof.
//
// The construction is deliberately the OpenZeppelin one — double-hashed leaves
// and sorted internal pairs — so the on-chain verifier is the audited
// MerkleProof library rather than something bespoke.

// Keccak256 is the hash the EVM speaks.
func Keccak256(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MerkleLeaf derives a certificate's leaf.
//
// The pair is hashed twice. A single hash would let an internal node be passed
// off as a leaf, since both are 32 bytes going into the same function; the
// second hash puts leaves in a domain no internal node can reach.
func MerkleLeaf(contentHash, featureCommitment [32]byte) [32]byte {
	inner := Keccak256(contentHash[:], featureCommitment[:])
	return Keccak256(inner[:])
}

// MerkleTree is a built batch: its root, and one inclusion proof per leaf in
// the order the leaves were given.
type MerkleTree struct {
	Root   [32]byte
	Leaves [][32]byte
	Proofs [][][32]byte
}

// BuildMerkleTree commits leaves to a single root and derives every proof.
//
// An odd node at any level is carried up unchanged rather than duplicated:
// duplicating it would make a lone leaf indistinguishable from a pair of
// identical leaves.
func BuildMerkleTree(leaves [][32]byte) *MerkleTree {
	if len(leaves) == 0 {
		return &MerkleTree{}
	}

	tree := &MerkleTree{
		Leaves: leaves,
		Proofs: make([][][32]byte, len(leaves)),
	}

	// positions[i] tracks where leaf i currently sits in the level being built,
	// so each leaf can collect the sibling it is hashed against.
	positions := make([]int, len(leaves))
	for i := range positions {
		positions[i] = i
	}

	level := leaves
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i]) // carried up
				continue
			}
			next = append(next, hashPair(level[i], level[i+1]))
		}

		for leafIdx, pos := range positions {
			// A carried-up node has no sibling at this level, so it
			// contributes nothing to the proof.
			if pos == len(level)-1 && len(level)%2 == 1 {
				positions[leafIdx] = pos / 2
				continue
			}
			sibling := pos ^ 1
			tree.Proofs[leafIdx] = append(tree.Proofs[leafIdx], level[sibling])
			positions[leafIdx] = pos / 2
		}

		level = next
	}

	tree.Root = level[0]
	return tree
}

// hashPair hashes two nodes in ascending byte order, which makes a proof
// independent of whether the sibling sat on the left or the right.
func hashPair(a, b [32]byte) [32]byte {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return Keccak256(a[:], b[:])
}

// VerifyMerkleProof recomputes the root from a leaf and its proof.
func VerifyMerkleProof(leaf, root [32]byte, proof [][32]byte) bool {
	computed := leaf
	for _, sibling := range proof {
		computed = hashPair(computed, sibling)
	}
	return computed == root
}

// EncodeProof renders a proof as 0x-prefixed hex for transport.
func EncodeProof(proof [][]byte) []string {
	out := make([]string, len(proof))
	for i, node := range proof {
		out[i] = "0x" + hex.EncodeToString(node)
	}
	return out
}

// FlattenProof converts a proof to the byte slices the repository persists.
func FlattenProof(proof [][32]byte) [][]byte {
	out := make([][]byte, len(proof))
	for i, node := range proof {
		n := node
		out[i] = n[:]
	}
	return out
}

// ProofFromBytes rebuilds a proof from stored byte slices, dropping anything
// that is not a 32-byte node.
func ProofFromBytes(raw [][]byte) [][32]byte {
	out := make([][32]byte, 0, len(raw))
	for _, node := range raw {
		if len(node) != 32 {
			continue
		}
		var n [32]byte
		copy(n[:], node)
		out = append(out, n)
	}
	return out
}
