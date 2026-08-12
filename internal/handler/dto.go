package handler

import (
	"net/http"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

type certDTO struct {
	ID          string `json:"id"`
	ContentHash string `json:"content_hash"`
	Registrant  string `json:"registrant"`
	TxHash      string `json:"tx_hash"`
	BlockNumber uint64 `json:"block_number"`
	CreatedAt   string `json:"created_at"`

	// Attested says whether the content was captured through an enrolled
	// device or merely uploaded. It is the single most important field for a
	// verifier deciding how much the certificate is worth.
	Attested bool `json:"attested"`
	// DeviceID and CapturedAt are present only for attested captures.
	DeviceID   string `json:"device_id,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`

	// Anchor carries the on-chain commitment once the batch is written. It is
	// absent while the certificate is still waiting for the next batch.
	Anchor *anchorDTO `json:"anchor,omitempty"`
}

// anchorDTO exposes the inclusion proof, so a verifier can check the
// certificate against the on-chain root without trusting this API.
type anchorDTO struct {
	TxHash      string   `json:"tx_hash"`
	BlockNumber uint64   `json:"block_number"`
	LeafIndex   int      `json:"leaf_index"`
	MerkleProof []string `json:"merkle_proof"`
}

func toCertDTO(c *domain.Certificate) certDTO {
	dto := certDTO{
		ID:          c.ID,
		ContentHash: c.ContentHash,
		Registrant:  c.Registrant,
		TxHash:      c.TxHash,
		BlockNumber: c.BlockNumber,
		CreatedAt:   c.CreatedAt.Format(time.RFC3339),
		Attested:    c.Attested(),
		DeviceID:    c.DeviceID,
	}
	if c.CapturedAt != nil {
		dto.CapturedAt = c.CapturedAt.Format(time.RFC3339Nano)
	}
	if c.Anchored() {
		dto.Anchor = &anchorDTO{
			TxHash:      c.TxHash,
			BlockNumber: c.BlockNumber,
			LeafIndex:   c.LeafIndex,
			MerkleProof: domain.EncodeProof(c.MerkleProof),
		}
	}
	return dto
}

type verifyDTO struct {
	Certified   bool     `json:"certified"`
	Certificate *certDTO `json:"certificate"`
}

func writeVerifyResponse(w http.ResponseWriter, out *usecase.VerifyOutput) {
	resp := verifyDTO{Certified: out.Certified}
	if out.Certificate != nil {
		dto := toCertDTO(out.Certificate)
		resp.Certificate = &dto
	}

	status := http.StatusOK
	if !out.Certified {
		status = http.StatusNotFound
	}
	writeJSON(w, status, resp)
}
