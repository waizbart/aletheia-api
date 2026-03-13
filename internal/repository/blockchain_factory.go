package repository

import (
	"fmt"
	"os"

	"github.com/waizbart/aletheia-api/internal/usecase"
)

func NewBlockchainServiceFromEnv() (usecase.BlockchainService, error) {
	rpcURL := os.Getenv("RPC_URL")
	fromAddress := os.Getenv("FROM_ADDRESS")
	contractAddress := os.Getenv("CONTRACT_ADDRESS")

	if rpcURL == "" || fromAddress == "" || contractAddress == "" {
		return nil, fmt.Errorf("RPC_URL, FROM_ADDRESS, and CONTRACT_ADDRESS are required")
	}

	return NewEVMBlockchainService(rpcURL, fromAddress, contractAddress)
}
