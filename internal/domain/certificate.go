package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type Certificate struct {
	ID          string
	ContentHash string
	Registrant  string
	TxHash      string
	BlockNumber uint64
	CreatedAt   time.Time
}

func HashContent(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hashing content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
