package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// FeatureCommitment is the 32-byte digest registered on chain alongside the
// content hash. It binds the off-chain feature bundle (pHash + ORB descriptors
// + ORB keypoints) to the on-chain certificate, so any later tampering of the
// stored bundle is detectable by recomputing this hash and comparing.
//
// Layout (sha256):
//
//	sha256( phash || sha256(orbDescriptors) || sha256(orbKeypoints) )
//
// Empty inputs are written as zero-length, so a non-image certificate (no
// pHash, no signature) yields a deterministic commitment over the empty string.
func FeatureCommitment(phash *[32]byte, sig *FeatureSignature) [32]byte {
	h := sha256.New()
	if phash != nil {
		h.Write(phash[:])
	}
	if sig != nil {
		desc := sha256.Sum256(sig.Descriptors)
		kp := sha256.Sum256(sig.Keypoints)
		h.Write(desc[:])
		h.Write(kp[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// FeatureCommitmentHex returns the commitment as a 64-char hex string with no
// 0x prefix, matching the format used elsewhere in the project for hashes.
func FeatureCommitmentHex(phash *[32]byte, sig *FeatureSignature) string {
	c := FeatureCommitment(phash, sig)
	return hex.EncodeToString(c[:])
}
