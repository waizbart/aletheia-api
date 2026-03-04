package repository

import (
	"fmt"
	"os"

	"github.com/waizbart/aletheia-api/internal/usecase"
)

func NewBlockchainServiceFromEnv() (usecase.BlockchainService, error) {
	rpcURL := os.Getenv("RPC_URL")
	fromAddress := os.Getenv("FROM_ADDRESS")
	if fromAddress == "" {
		fromAddress = os.Getenv("PRIVATE_KEY")
	}
	contractAddress := os.Getenv("CONTRACT_ADDRESS")

	if rpcURL == "" || fromAddress == "" || contractAddress == "" {
		return NewStubBlockchainService(), nil
	}

	svc, err := NewEVMBlockchainService(rpcURL, fromAddress, contractAddress)
	if err != nil {
		return nil, fmt.Errorf("create evm blockchain service: %w", err)
	}
	return svc, nil
}
