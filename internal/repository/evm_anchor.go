package repository

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// anchorSelector is the 4-byte selector of AnchorRegistry.anchor(bytes32,uint64).
// Computed once from the canonical signature rather than hard-coded, so a
// change to the contract's ABI cannot silently drift from this client.
var anchorSelector = func() []byte {
	h := domain.Keccak256([]byte("anchor(bytes32,uint64)"))
	return h[:4]
}()

// EVMAnchorService writes Merkle roots to the anchor contract.
//
// It signs locally and broadcasts with eth_sendRawTransaction. The previous
// implementation used eth_sendTransaction, which requires the node to hold an
// unlocked account: that works against a local Anvil and against no hosted RPC
// provider, so it could never have reached production.
type EVMAnchorService struct {
	rpcURL     string
	contract   string
	privateKey *secp256k1.PrivateKey
	from       string
	chainID    *big.Int
	gasLimit   uint64
	httpClient *http.Client

	// receiptTimeout bounds how long we wait for a transaction to be mined
	// before giving up and leaving the anchor pending for a later pass.
	receiptTimeout time.Duration
	pollInterval   time.Duration
}

// AnchorConfig configures the anchor client.
type AnchorConfig struct {
	RPCURL          string
	ContractAddress string
	// PrivateKeyHex is the anchoring account's secp256k1 key, with or without
	// a 0x prefix.
	PrivateKeyHex  string
	ChainID        uint64
	GasLimit       uint64
	ReceiptTimeout time.Duration
	PollInterval   time.Duration
}

func NewEVMAnchorService(cfg AnchorConfig) (*EVMAnchorService, error) {
	if cfg.RPCURL == "" {
		return nil, fmt.Errorf("anchor: rpc url is required")
	}
	if !isHexAddress(cfg.ContractAddress) {
		return nil, fmt.Errorf("anchor: invalid contract address: %s", cfg.ContractAddress)
	}

	raw, err := hex.DecodeString(strings.TrimPrefix(cfg.PrivateKeyHex, "0x"))
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("anchor: private key must be 32 bytes of hex")
	}
	key := secp256k1.PrivKeyFromBytes(raw)

	if cfg.ChainID == 0 {
		return nil, fmt.Errorf("anchor: chain id is required")
	}
	if cfg.GasLimit == 0 {
		cfg.GasLimit = 120_000
	}
	if cfg.ReceiptTimeout <= 0 {
		cfg.ReceiptTimeout = 3 * time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 3 * time.Second
	}

	return &EVMAnchorService{
		rpcURL:         cfg.RPCURL,
		contract:       cfg.ContractAddress,
		privateKey:     key,
		from:           addressFromKey(key),
		chainID:        new(big.Int).SetUint64(cfg.ChainID),
		gasLimit:       cfg.GasLimit,
		httpClient:     &http.Client{Timeout: rpcTimeout},
		receiptTimeout: cfg.ReceiptTimeout,
		pollInterval:   cfg.PollInterval,
	}, nil
}

// From reports the anchoring account's address, so an operator can fund it.
func (s *EVMAnchorService) From() string { return s.from }

// RegisterRoot anchors a Merkle root and waits for its receipt.
//
// It returns only once the transaction is mined, so a recorded block number is
// always a real one. The old client returned a block number of zero for every
// certificate because it never fetched a receipt.
func (s *EVMAnchorService) RegisterRoot(ctx context.Context, root [32]byte, leafCount uint64) (string, uint64, error) {
	nonce, err := s.pendingNonce(ctx)
	if err != nil {
		return "", 0, err
	}
	gasPrice, err := s.gasPrice(ctx)
	if err != nil {
		return "", 0, err
	}

	raw, err := s.signAnchorTx(nonce, gasPrice, root, leafCount)
	if err != nil {
		return "", 0, err
	}

	var txHash string
	if err := s.call(ctx, "eth_sendRawTransaction", []any{"0x" + hex.EncodeToString(raw)}, &txHash); err != nil {
		return "", 0, err
	}

	blockNumber, err := s.waitForReceipt(ctx, txHash)
	if err != nil {
		// The transaction is out there; we just could not confirm it in time.
		// Report the hash so the batch can be reconciled later rather than
		// re-anchored.
		return txHash, 0, err
	}
	return txHash, blockNumber, nil
}

// calldata builds anchor(bytes32 root, uint64 leafCount).
func calldata(root [32]byte, leafCount uint64) []byte {
	data := make([]byte, 0, 4+64)
	data = append(data, anchorSelector...)
	data = append(data, root[:]...)

	var countWord [32]byte
	binary.BigEndian.PutUint64(countWord[24:], leafCount)
	return append(data, countWord[:]...)
}

// signAnchorTx builds and signs an EIP-155 legacy transaction.
func (s *EVMAnchorService) signAnchorTx(nonce uint64, gasPrice *big.Int, root [32]byte, leafCount uint64) ([]byte, error) {
	to, err := hex.DecodeString(strings.TrimPrefix(s.contract, "0x"))
	if err != nil {
		return nil, fmt.Errorf("anchor: decoding contract address: %w", err)
	}
	data := calldata(root, leafCount)

	// EIP-155: the chain id is folded into the signed payload so a signature
	// cannot be replayed onto a different chain.
	signingPayload := rlpList(
		rlpUint(nonce),
		rlpBig(gasPrice),
		rlpUint(s.gasLimit),
		rlpString(to),
		rlpBig(nil), // value: anchoring transfers nothing
		rlpString(data),
		rlpBig(s.chainID),
		rlpString(nil),
		rlpString(nil),
	)

	digest := domain.Keccak256(signingPayload)
	sig := ecdsa.SignCompact(s.privateKey, digest[:], false)
	if len(sig) != 65 {
		return nil, fmt.Errorf("anchor: unexpected signature length %d", len(sig))
	}

	// SignCompact returns [recoveryHeader || R || S] with the header offset by
	// 27. Ethereum wants v = recovery + 35 + 2*chainID.
	recovery := int64(sig[0]) - 27
	v := new(big.Int).Add(
		new(big.Int).Mul(s.chainID, big.NewInt(2)),
		big.NewInt(35+recovery),
	)

	return rlpList(
		rlpUint(nonce),
		rlpBig(gasPrice),
		rlpUint(s.gasLimit),
		rlpString(to),
		rlpBig(nil),
		rlpString(data),
		rlpBig(v),
		rlpBig(new(big.Int).SetBytes(sig[1:33])), // R
		rlpBig(new(big.Int).SetBytes(sig[33:65])), // S
	), nil
}

// waitForReceipt polls until the transaction is mined or the budget runs out.
func (s *EVMAnchorService) waitForReceipt(ctx context.Context, txHash string) (uint64, error) {
	deadline := time.Now().Add(s.receiptTimeout)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		var receipt struct {
			BlockNumber string `json:"blockNumber"`
			Status      string `json:"status"`
		}
		if err := s.call(ctx, "eth_getTransactionReceipt", []any{txHash}, &receipt); err == nil && receipt.BlockNumber != "" {
			// status 0x0 means the transaction was mined but reverted, which
			// is a very different thing from "not yet mined".
			if receipt.Status == "0x0" {
				return 0, fmt.Errorf("anchor: transaction %s reverted", txHash)
			}
			n, err := parseHexUint(receipt.BlockNumber)
			if err != nil {
				return 0, fmt.Errorf("anchor: unreadable block number %q: %w", receipt.BlockNumber, err)
			}
			return n, nil
		}

		if time.Now().After(deadline) {
			return 0, fmt.Errorf("anchor: transaction %s not mined within %s", txHash, s.receiptTimeout)
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *EVMAnchorService) pendingNonce(ctx context.Context) (uint64, error) {
	var raw string
	if err := s.call(ctx, "eth_getTransactionCount", []any{s.from, "pending"}, &raw); err != nil {
		return 0, err
	}
	return parseHexUint(raw)
}

func (s *EVMAnchorService) gasPrice(ctx context.Context) (*big.Int, error) {
	var raw string
	if err := s.call(ctx, "eth_gasPrice", []any{}, &raw); err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(raw, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("anchor: unreadable gas price %q", raw)
	}
	return v, nil
}

// call performs one JSON-RPC round trip and unmarshals the result.
func (s *EVMAnchorService) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("anchor: marshalling %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.rpcURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("anchor: building %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("anchor: %s: %w", method, err)
	}
	defer resp.Body.Close()

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("anchor: decoding %s response: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("anchor: %s: %s", method, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return fmt.Errorf("anchor: %s returned no result", method)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("anchor: reading %s result: %w", method, err)
	}
	return nil
}

// addressFromKey derives the EVM address of a key: the last 20 bytes of the
// keccak of the uncompressed public key, minus its 0x04 prefix.
func addressFromKey(key *secp256k1.PrivateKey) string {
	pub := key.PubKey().SerializeUncompressed()
	sum := domain.Keccak256(pub[1:])
	return "0x" + hex.EncodeToString(sum[12:])
}

func parseHexUint(raw string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(raw, "0x"), 16, 64)
}

// rpcTimeout bounds every JSON-RPC round trip. Without it, a node that accepts
// the connection and then stalls would pin the caller indefinitely.
const rpcTimeout = 30 * time.Second

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
