package usecase

import (
	"context"
	"io"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type CertificateRepository interface {
	Save(ctx context.Context, cert *domain.Certificate) error
	FindByHash(ctx context.Context, contentHash string) (*domain.Certificate, error)
	// FindCandidatesByPHashes returns perceptual candidates of the given media
	// kind only. The partition is load-bearing: image and video pHashes are not
	// comparable, so a query that ignored it would return noise and, worse,
	// could answer an image upload with a video's certificate.
	FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, mediaKind domain.MediaKind, maxDistance, topK int) ([]*domain.Certificate, error)
	Delete(ctx context.Context, contentHash string) error
}

// BlockchainService commits a batch of certificates to the chain.
type BlockchainService interface {
	// RegisterRoot anchors a Merkle root covering leafCount certificates. It
	// must not return until the transaction has a receipt, so a recorded block
	// number is always a real one.
	//
	// An implementation that broadcast a transaction but could not confirm it
	// must return the transaction hash alongside the error. That transaction
	// may still be mined, and the hash is the only handle an operator has on
	// it; discarding it leaves an unaccounted-for root on chain.
	RegisterRoot(ctx context.Context, root [32]byte, leafCount uint64) (txHash string, blockNum uint64, err error)
}

// AnchorRepository drives the batching anchor worker.
type AnchorRepository interface {
	// PendingLeaves returns certificates awaiting an anchor, oldest first.
	PendingLeaves(ctx context.Context, limit int) ([]*domain.Certificate, error)
	// SaveAnchor records the batch and attaches each certificate's inclusion
	// proof in one transaction, so a certificate is never left claiming
	// membership in a batch that was not written.
	SaveAnchor(ctx context.Context, a *domain.Anchor, leaves []*domain.Certificate) error
	// SaveUnconfirmedAnchor records a broadcast transaction that could not be
	// confirmed, with no certificates attached. It exists so an operator can
	// reconcile a root that may yet be mined, rather than discovering it on
	// chain with nothing in the database referring to it.
	SaveUnconfirmedAnchor(ctx context.Context, a *domain.Anchor) error
}

// VideoHandle is a video that has been taken in and is ready to decode. The
// caller owns it and must Close it exactly once: that releases the decoder's
// temporary file and the extractor's decode slot, neither of which any other
// code can reach.
type VideoHandle interface {
	ContentHash() string
	// Probe reports what the container header claims. It is metadata and may be
	// wrong, so it decides whether to reject the video early — never what the
	// certificate asserts.
	Probe() domain.VideoProbe
	Close() error
}

// VideoReduction is what a whole video collapses to: the sequence of frame
// hashes, plus one anchor frame carrying the ordinary image signature.
type VideoReduction struct {
	// Signature is the frame sequence and the chosen anchor index. Persisted.
	Signature *domain.VideoSignature
	// Anchor is the anchor frame's ORB descriptors and color grid. Persisted in
	// the certificate's ordinary feature columns, which is what lets the image
	// matcher run against video unchanged.
	Anchor *domain.FeatureSignature
	// AnchorPHash is the anchor frame's perceptual hash. Persisted, and indexed
	// in phash_bands like any image.
	AnchorPHash [32]byte
	// AnchorFrame is the anchor frame encoded as PNG. It is transient: verify
	// feeds it to FeatureExtractor.Match as the candidate image and then drops
	// it. Nothing about a video is ever stored.
	AnchorFrame []byte
}

// VideoFeatureExtractor decodes video. It is split in two so the expensive half
// can be refused before it runs: Ingest reads only the container header, which
// is enough to reject an overlong or oversized video and to run the duplicate
// check, and Reduce is what actually decodes frames.
type VideoFeatureExtractor interface {
	// Ingest streams r to wherever the decoder can reach it, hashing as it
	// goes, and reads the container header. The reader is never held in memory:
	// a video is far too large for that, and the hash has to be known before the
	// decode is paid for.
	Ingest(ctx context.Context, r io.Reader) (VideoHandle, error)
	// Reduce decodes the sampled frames and builds the stored signature.
	Reduce(ctx context.Context, h VideoHandle) (*VideoReduction, error)
}

type FeatureExtractor interface {
	// Compute extracts the full stored signature from an image: ORB keypoints
	// and descriptors plus the color grid (per-cell LAB means) and reference
	// dimensions the matcher's color-residual gate reads at verify time.
	Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, error)
	// Match compares a stored reference signature against a candidate. Only the
	// candidate image bytes are needed — the reference side runs entirely from
	// the signature, so no reference image is stored or fetched anywhere.
	Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error)
}

// ColorGridRenderer renders a stored color grid back to a small PNG so the
// observability dashboard can show candidate thumbnails without any stored
// reference image.
type ColorGridRenderer interface {
	RenderColorGridPNG(grid []byte, refWidth, refHeight int) ([]byte, error)
}

// DeviceRepository persists enrolled capture devices.
type DeviceRepository interface {
	Save(ctx context.Context, d *domain.Device) error
	FindByID(ctx context.Context, id string) (*domain.Device, error)
	// FindByPublicKey looks a device up by its attested key, which is the
	// device's real identity. Returns (nil, nil) when the key is unknown.
	FindByPublicKey(ctx context.Context, publicKey []byte) (*domain.Device, error)
	ListByOrg(ctx context.Context, orgID string) ([]*domain.Device, error)
	// Revoke flags the device unusable for new captures. Certificates it
	// already produced are left untouched.
	Revoke(ctx context.Context, id, reason string, at time.Time) error
}

// NonceRepository persists capture challenges.
type NonceRepository interface {
	Save(ctx context.Context, n domain.CaptureNonce) error
	// Consume atomically marks a challenge spent and returns it. The atomicity
	// is the whole point: two concurrent captures presenting the same nonce
	// must not both succeed. Returns domain.ErrNonceUnusable when the challenge
	// is unknown, already spent or expired.
	Consume(ctx context.Context, value string, now time.Time) (*domain.CaptureNonce, error)
	// DeleteExpired prunes challenges past their window.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// AttestationVerifier proves a capture key lives in genuine secure hardware.
type AttestationVerifier interface {
	Verify(ctx context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error)
}

// OrgRepository persists tenants and their API credentials.
type OrgRepository interface {
	SaveOrg(ctx context.Context, o *domain.Org) error
	FindOrgByID(ctx context.Context, id string) (*domain.Org, error)
	SaveAPIKey(ctx context.Context, k *domain.APIKey) error
	// FindOrgByAPIKeyHash resolves a presented credential to its owner. Lookup
	// is by hash so the database never holds a usable credential.
	FindOrgByAPIKeyHash(ctx context.Context, hash string) (*domain.Org, *domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id string, at time.Time) error
}

// UsageRepository counts billable operations.
type UsageRepository interface {
	// Record increments the counter for an org, operation and billing period.
	Record(ctx context.Context, orgID string, op domain.Operation, at time.Time) error
	// CountForPeriod returns how many of op the org performed in the billing
	// period containing at.
	CountForPeriod(ctx context.Context, orgID string, op domain.Operation, at time.Time) (int, error)
	// Summary returns every operation's count for the period containing at.
	Summary(ctx context.Context, orgID string, at time.Time) (map[domain.Operation]int, error)
}

// CertifyRunner is the certification step an attested capture delegates to once
// the device and signature have been checked.
type CertifyRunner interface {
	Execute(ctx context.Context, in CertifyInput) (*CertifyOutput, error)
}
