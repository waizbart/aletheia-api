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
}

func toCertDTO(c *domain.Certificate) certDTO {
	return certDTO{
		ID:          c.ID,
		ContentHash: c.ContentHash,
		Registrant:  c.Registrant,
		TxHash:      c.TxHash,
		BlockNumber: c.BlockNumber,
		CreatedAt:   c.CreatedAt.Format(time.RFC3339),
	}
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
