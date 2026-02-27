package repository

import (
	"context"
	"fmt"
	"log"
)

type StubBlockchainService struct{}

func NewStubBlockchainService() *StubBlockchainService {
	return &StubBlockchainService{}
}

func (s *StubBlockchainService) RegisterHash(ctx context.Context, hash string) (string, uint64, error) {
	log.Printf("[stub-blockchain] RegisterHash called for %s", hash)
	fakeTx := fmt.Sprintf("0x%064s", hash[:16])
	return fakeTx, 0, nil
}

func (s *StubBlockchainService) IsHashRegistered(ctx context.Context, hash string) (bool, error) {
	log.Printf("[stub-blockchain] IsHashRegistered called for %s", hash)
	return false, nil
}
