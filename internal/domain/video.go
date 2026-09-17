package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// MediaKind is what a certificate covers. It is stored on the certificate and
// it partitions the perceptual index: image and video pHashes are computed in
// different metric spaces (see VideoFrameSamples) and must never be compared
// against each other.
type MediaKind string

const (
	MediaKindImage MediaKind = "image"
	MediaKindVideo MediaKind = "video"
)

// ValidMediaKind reports whether k is a kind the registry certifies.
func ValidMediaKind(k MediaKind) bool {
	switch k {
	case MediaKindImage, MediaKindVideo:
		return true
	}
	return false
}

const (
	// VideoFrameSamples is how many frames a video is reduced to. 32 pHashes
	// cost 1 KB per certificate, trivial beside the 48 KB color grid already on
	// the row, and give one sample every 1.9s of a 60s video.
	//
	// Frames are resized to ResizeMax before hashing, unlike PHash256, whose
	// internal nearest-neighbour downsample picks visibly different pixels
	// starting from 3840px than from 1024px — a 4K to 1080p transcode would
	// otherwise drift. That normalization is why video frame pHashes are not
	// comparable with image pHashes and why the candidate lookup is partitioned
	// by MediaKind.
	VideoFrameSamples = 32

	// MaxFramePHashDistance is the per-frame Hamming budget, out of 256 bits.
	//
	// MaxPHashDistance (96) is a recall-first prefilter threshold and is
	// deliberately loose; a gate needs precision instead. Calibrate against
	// testdata/curated/video before trusting this value and record the measured
	// distribution here.
	MaxFramePHashDistance = 64

	// MinFrameAgreement is the fraction of sampled frames that must land within
	// MaxFramePHashDistance for the sequence to be considered the same video:
	// 23 of 32. A re-encode plus one slot of drift reliably breaks a handful of
	// slots that fall on hard cuts or fast pans, where a one-frame temporal
	// error is a large pixel error. 0.90 is brittle; 0.50 is not a gate.
	MinFrameAgreement = 0.70

	// FrameAlignSlack is how many slots either side of its own index a
	// candidate frame may match. Proportional sampling is fps-invariant within
	// a file, but a transcode that drops trailing frames still shifts the grid
	// slightly. One slot absorbs that; more would start accepting trims, which
	// whole-file semantics must reject.
	FrameAlignSlack = 1

	// MaxDurationDriftRatio and MinDurationDriftMs bound how far a candidate's
	// duration may sit from the reference. This gate is deliberately weak — a
	// 4% trim of a ten-minute video passes it — because its job is only to
	// reject obviously different files cheaply. The frame sequence is what
	// rejects a trim, since shifting every sample point breaks far more than
	// one slot of slack.
	MaxDurationDriftRatio = 0.05
	MinDurationDriftMs    = 500

	// MaxVideoDurationMs caps decode cost, which is linear in duration. Two
	// minutes of 1080p30 is ~3600 frames, roughly 6-12s on one core, which fits
	// a synchronous request. Five minutes would not.
	MaxVideoDurationMs = 120_000

	// MaxVideoPixels rejects decoder bombs: a 16384x16384 stream allocates
	// hundreds of megabytes per frame buffer inside libavcodec. The gate is
	// partial by nature — probing the container already instantiates a decoder
	// context — so it reduces exposure rather than removing it.
	MaxVideoPixels = 3840 * 2160
)

// VideoProbe is what the container header reports before a single frame is
// decoded. Every field is metadata and may be wrong — FrameCount on a
// variable-frame-rate recording is an estimate — so it is used to reject
// obviously unacceptable input early, never as ground truth.
type VideoProbe struct {
	DurationMs int
	Width      int
	Height     int
	FrameCount int
	FPS        float64
	SizeBytes  int64
}

// Validate applies the policy limits. It runs after the container header is
// read and before any frame is decoded, so an oversized or overlong video
// costs a header parse rather than a full decode.
func (p VideoProbe) Validate() error {
	if p.DurationMs <= 0 {
		return fmt.Errorf("video probe: duration is %dms: %w", p.DurationMs, ErrVideoUndecodable)
	}
	if p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("video probe: dimensions are %dx%d: %w", p.Width, p.Height, ErrVideoUndecodable)
	}
	if p.DurationMs > MaxVideoDurationMs {
		return fmt.Errorf("video probe: %dms exceeds the %dms limit: %w",
			p.DurationMs, MaxVideoDurationMs, ErrVideoTooLong)
	}
	if p.Width*p.Height > MaxVideoPixels {
		return fmt.Errorf("video probe: %dx%d exceeds the %d pixel limit: %w",
			p.Width, p.Height, MaxVideoPixels, ErrVideoResolution)
	}
	return nil
}

// VideoSignature is the video-specific half of a certificate: the perceptual
// hash of every sampled frame, which index among them was chosen as the anchor,
// and the duration the sampling was derived from.
//
// The anchor frame's own ORB descriptors and color grid live in the certificate's
// ordinary FeatureSignature, so the image matcher applies to video unchanged.
type VideoSignature struct {
	FramePHashes [][32]byte
	AnchorIndex  int
	DurationMs   int
}

// FrameCount returns how many frames were sampled. It can be below
// VideoFrameSamples when decoding ended early, which is recorded rather than
// padded.
func (v *VideoSignature) FrameCount() int {
	if v == nil {
		return 0
	}
	return len(v.FramePHashes)
}

// Valid reports whether the signature carries a usable frame sequence with an
// in-range anchor.
func (v *VideoSignature) Valid() bool {
	return v != nil &&
		len(v.FramePHashes) > 0 &&
		v.AnchorIndex >= 0 &&
		v.AnchorIndex < len(v.FramePHashes)
}

// SampleFrameIndexes returns the frame numbers to sample from a video of
// frameCount frames, one per slot, at the midpoint of each slot:
//
//	n_i = round(frameCount * (2i+1) / (2*VideoFrameSamples))
//
// Midpoints rather than i/K for two reasons: slot 0 would otherwise land on
// frame 0, which is the frame most likely to be a fade-in or an auto-exposure
// ramp, and the final 1/K of the video would never be sampled at all.
//
// The arithmetic is integer-only so certify and verify derive byte-identical
// indexes on any platform. Non-deterministic sampling is the one thing this
// design cannot tolerate: the two sides must agree on which frames to compare.
//
// Sampling by proportion of frameCount is invariant to frame rate within a
// file, which is the property that matters: a 30-to-25fps transcode changes the
// count from 300 to 250, but slot 7 lands at the same wall-clock instant in
// both. A video with fewer frames than slots maps several slots onto the same
// frame, which is correct and needs no special case.
func SampleFrameIndexes(frameCount int) []int {
	if frameCount <= 0 {
		return nil
	}

	const k = VideoFrameSamples
	out := make([]int, k)
	for i := 0; i < k; i++ {
		// Round half up without floating point: (n*(2i+1) + k) / (2k).
		idx := (frameCount*(2*i+1) + k) / (2 * k)
		if idx >= frameCount {
			idx = frameCount - 1
		}
		out[i] = idx
	}
	return out
}

// EncodeFramePHashes flattens a frame sequence for storage as a single BYTEA.
// The hashes are always read as a unit — nothing queries an individual frame,
// because excerpt search is out of scope — so a column beats a table.
func EncodeFramePHashes(hashes [][32]byte) []byte {
	out := make([]byte, 0, len(hashes)*32)
	for _, h := range hashes {
		out = append(out, h[:]...)
	}
	return out
}

// DecodeFramePHashes rebuilds a frame sequence from storage. A trailing partial
// hash is dropped rather than reported: the same tolerance the certificate
// columns already apply to a wrong-length pHash blob.
func DecodeFramePHashes(raw []byte) [][32]byte {
	n := len(raw) / 32
	if n == 0 {
		return nil
	}
	out := make([][32]byte, n)
	for i := 0; i < n; i++ {
		copy(out[i][:], raw[i*32:(i+1)*32])
	}
	return out
}

// FrameSequenceScore measures how much of a candidate's frame sequence the
// reference accounts for, returning the fraction matched and the raw count.
//
// Each candidate slot is compared against the reference slots within
// FrameAlignSlack of its own index and keeps the best of them. That is a fixed
// local window, not temporal alignment: it absorbs the grid drift a transcode
// introduces without ever accepting content that sits elsewhere on the timeline.
func FrameSequenceScore(ref, cand [][32]byte) (agreement float64, matched int) {
	if len(ref) == 0 || len(cand) == 0 {
		return 0, 0
	}

	for i, c := range cand {
		best := 256
		for off := -FrameAlignSlack; off <= FrameAlignSlack; off++ {
			j := i + off
			if j < 0 || j >= len(ref) {
				continue
			}
			if d := Hamming256(c, ref[j]); d < best {
				best = d
			}
		}
		if best <= MaxFramePHashDistance {
			matched++
		}
	}

	return float64(matched) / float64(len(cand)), matched
}

// DurationWithinTolerance reports whether a candidate's duration is close
// enough to the reference's to be the same file re-encoded.
//
// The absolute floor matters for short videos, where a percentage of very
// little is a window too narrow for ordinary container rounding. A reference
// with no known duration fails closed: there is nothing to compare against.
func DurationWithinTolerance(refMs, candMs int) bool {
	if refMs <= 0 || candMs <= 0 {
		return false
	}

	diff := refMs - candMs
	if diff < 0 {
		diff = -diff
	}

	tolerance := MinDurationDriftMs
	if ratio := int(float64(refMs) * MaxDurationDriftRatio); ratio > tolerance {
		tolerance = ratio
	}
	return diff <= tolerance
}

// DecideVideo is the video verdict: the anchor frame must pass the ordinary
// image gates, and the frame sequence and duration must agree.
//
// The sequence gate is necessary but not sufficient. In a locked-off shot every
// frame is near-identical, so agreement carries no information and two
// different recordings of the same static scene both score highly; the anchor's
// ORB and color match is what discriminates. The image pipeline has the same
// property for two photographs of one scene.
func DecideVideo(anchor MatchDecision, agreement float64, durationOK bool) bool {
	return anchor.Matched &&
		agreement >= MinFrameAgreement &&
		durationOK
}

// videoCommitmentDomain separates a video commitment from an image one, so the
// two can never collide even when a video's anchor frame and a certified image
// produce identical features.
const videoCommitmentDomain = "aletheia-video-commitment-v1"

// VideoFeatureCommitment is the 32-byte digest a video certificate anchors on
// chain. It wraps the ordinary FeatureCommitment of the anchor frame and adds
// every other value the certificate asserts about the video.
//
// Layout:
//
//	sha256( "aletheia-video-commitment-v1"
//	     || uint16BE(anchorIndex)
//	     || uint16BE(frameCount)
//	     || uint32BE(durationMs)
//	     || FeatureCommitment(anchorPHash, anchorSig)
//	     || sha256(concat(framePHashes)) )
//
// The extra fields are not decoration. Without them an operator could rewrite
// anchor_index or duration_ms and the Merkle proof would still verify, which in
// a registry whose whole value is tamper-evidence is an integrity hole.
func VideoFeatureCommitment(anchorPHash *[32]byte, anchorSig *FeatureSignature, v *VideoSignature) [32]byte {
	h := sha256.New()
	h.Write([]byte(videoCommitmentDomain))

	var header [8]byte
	if v != nil {
		binary.BigEndian.PutUint16(header[0:2], uint16(v.AnchorIndex))
		binary.BigEndian.PutUint16(header[2:4], uint16(len(v.FramePHashes)))
		binary.BigEndian.PutUint32(header[4:8], uint32(v.DurationMs))
	}
	h.Write(header[:])

	anchor := FeatureCommitment(anchorPHash, anchorSig)
	h.Write(anchor[:])

	var frames [32]byte
	if v != nil {
		frames = sha256.Sum256(EncodeFramePHashes(v.FramePHashes))
	} else {
		frames = sha256.Sum256(nil)
	}
	h.Write(frames[:])

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
