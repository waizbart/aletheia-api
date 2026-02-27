package usecase_test

import (
	"context"
	"errors"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

type mockRepo struct {
	saveFn       func(ctx context.Context, cert *domain.Certificate) error
	findByHashFn func(ctx context.Context, hash string) (*domain.Certificate, error)
}

func (m *mockRepo) Save(ctx context.Context, cert *domain.Certificate) error {
	return m.saveFn(ctx, cert)
}

func (m *mockRepo) FindByHash(ctx context.Context, hash string) (*domain.Certificate, error) {
	return m.findByHashFn(ctx, hash)
}

type mockBlockchain struct {
	registerHashFn     func(ctx context.Context, hash string) (string, uint64, error)
	isHashRegisteredFn func(ctx context.Context, hash string) (bool, error)
}

func (m *mockBlockchain) RegisterHash(ctx context.Context, hash string) (string, uint64, error) {
	return m.registerHashFn(ctx, hash)
}

func (m *mockBlockchain) IsHashRegistered(ctx context.Context, hash string) (bool, error) {
	return m.isHashRegisteredFn(ctx, hash)
}
