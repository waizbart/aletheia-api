package repository

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// NewAnchorServiceFromEnv builds the anchor client from configuration.
//
//	RPC_URL                 EVM JSON-RPC endpoint
//	CONTRACT_ADDRESS        deployed AnchorRegistry
//	ANCHOR_PRIVATE_KEY      secp256k1 key of the anchoring account
//	CHAIN_ID                EIP-155 chain id (137 for Polygon, 31337 for Anvil)
//	ANCHOR_GAS_LIMIT        optional, defaults to 120000
func NewAnchorServiceFromEnv() (*EVMAnchorService, error) {
	chainID, err := strconv.ParseUint(os.Getenv("CHAIN_ID"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("anchor: CHAIN_ID must be a number: %w", err)
	}

	gasLimit := uint64(0)
	if raw := os.Getenv("ANCHOR_GAS_LIMIT"); raw != "" {
		gasLimit, err = strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("anchor: ANCHOR_GAS_LIMIT must be a number: %w", err)
		}
	}

	return NewEVMAnchorService(AnchorConfig{
		RPCURL:          os.Getenv("RPC_URL"),
		ContractAddress: os.Getenv("CONTRACT_ADDRESS"),
		PrivateKeyHex:   os.Getenv("ANCHOR_PRIVATE_KEY"),
		ChainID:         chainID,
		GasLimit:        gasLimit,
		ReceiptTimeout:  3 * time.Minute,
	})
}
