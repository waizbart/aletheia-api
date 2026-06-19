package source

import (
	"strings"
	"testing"
)

func TestReadLimitedRejectsOversizeResponse(t *testing.T) {
	_, err := readLimited(strings.NewReader("12345"), 4)
	if err == nil {
		t.Fatal("expected oversize response error, got nil")
	}
}
