package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestHashContent_Success(t *testing.T) {
	want := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	got, err := domain.HashContent(strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestHashContent_EmptyReader(t *testing.T) {
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	got, err := domain.HashContent(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

func TestHashContent_ErrorReader(t *testing.T) {
	_, err := domain.HashContent(errReader{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
