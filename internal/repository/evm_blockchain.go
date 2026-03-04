package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type RPCBlockchainService struct {
	rpcURL      string
	fromAddress string
	toAddress   string
	httpClient  *http.Client
}

func NewEVMBlockchainService(rpcURL, fromAddress, anchorAddress string) (*RPCBlockchainService, error) {
	if !isHexAddress(fromAddress) {
		return nil, fmt.Errorf("invalid from address: %s", fromAddress)
	}
	if !isHexAddress(anchorAddress) {
		return nil, fmt.Errorf("invalid anchor address: %s", anchorAddress)
	}
	if rpcURL == "" {
		return nil, fmt.Errorf("rpc url is required")
	}

	return &RPCBlockchainService{
		rpcURL:      rpcURL,
		fromAddress: fromAddress,
		toAddress:   anchorAddress,
		httpClient:  &http.Client{},
	}, nil
}

func (s *RPCBlockchainService) RegisterHash(ctx context.Context, hash string) (string, uint64, error) {
	data, err := normalizeHashToBytes(hash)
	if err != nil {
		return "", 0, err
	}

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_sendTransaction",
		"params": []map[string]string{{
			"from": s.fromAddress,
			"to":   s.toAddress,
			"data": "0x" + hex.EncodeToString(data),
		}},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("marshal rpc request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.rpcURL, bytes.NewReader(payload))
	if err != nil {
		return "", 0, fmt.Errorf("create rpc request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("send rpc request: %w", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", 0, fmt.Errorf("decode rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return "", 0, fmt.Errorf("rpc error: %s", rpcResp.Error.Message)
	}
	if rpcResp.Result == "" {
		return "", 0, fmt.Errorf("rpc error: empty transaction hash")
	}

	return rpcResp.Result, 0, nil
}

func (s *RPCBlockchainService) IsHashRegistered(context.Context, string) (bool, error) {
	return false, nil
}

func normalizeHashToBytes(hash string) ([]byte, error) {
	normalized := strings.TrimPrefix(hash, "0x")
	if len(normalized) != 64 {
		return nil, fmt.Errorf("hash must be 32-byte hex")
	}
	data, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("decode hash: %w", err)
	}
	return data, nil
}

func isHexAddress(v string) bool {
	if len(v) != 42 || !strings.HasPrefix(v, "0x") {
		return false
	}
	for _, c := range v[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
