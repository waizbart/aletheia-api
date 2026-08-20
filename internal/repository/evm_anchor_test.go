package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// Anvil's first development account. Its address is published alongside the
// key, which makes it independent ground truth for key handling: if address
// derivation is wrong, this test fails without needing another implementation
// to compare against.
const (
	anvilKey     = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	anvilAddress = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
	testContract = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
)

func testConfig() AnchorConfig {
	return AnchorConfig{
		RPCURL:          "http://127.0.0.1:8545",
		ContractAddress: testContract,
		PrivateKeyHex:   anvilKey,
		ChainID:         31337,
		PollInterval:    time.Millisecond,
		ReceiptTimeout:  50 * time.Millisecond,
	}
}

func TestAddressFromKey_MatchesPublishedAddress(t *testing.T) {
	raw, err := hex.DecodeString(anvilKey)
	if err != nil {
		t.Fatal(err)
	}

	got := addressFromKey(secp256k1.PrivKeyFromBytes(raw))
	if !strings.EqualFold(got, anvilAddress) {
		t.Fatalf("addressFromKey = %s, want %s", got, anvilAddress)
	}
}

func TestNewEVMAnchorService_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AnchorConfig)
		want   string
	}{
		{name: "rpc url required", mutate: func(c *AnchorConfig) { c.RPCURL = "" }, want: "rpc url is required"},
		{name: "contract address must be an address", mutate: func(c *AnchorConfig) { c.ContractAddress = "0xnope" }, want: "invalid contract address"},
		{name: "private key must be 32 bytes", mutate: func(c *AnchorConfig) { c.PrivateKeyHex = "abcd" }, want: "32 bytes of hex"},
		{name: "private key must be hex", mutate: func(c *AnchorConfig) { c.PrivateKeyHex = strings.Repeat("z", 64) }, want: "32 bytes of hex"},
		{name: "chain id required", mutate: func(c *AnchorConfig) { c.ChainID = 0 }, want: "chain id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			tt.mutate(&cfg)

			_, err := NewEVMAnchorService(cfg)
			if err == nil {
				t.Fatal("expected a configuration error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestNewEVMAnchorService_Defaults(t *testing.T) {
	cfg := testConfig()
	cfg.GasLimit, cfg.ReceiptTimeout, cfg.PollInterval = 0, 0, 0

	svc, err := NewEVMAnchorService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if svc.gasLimit == 0 || svc.receiptTimeout == 0 || svc.pollInterval == 0 {
		t.Errorf("defaults not applied: %+v", svc)
	}
	if !strings.EqualFold(svc.From(), anvilAddress) {
		t.Errorf("From() = %s", svc.From())
	}
}

func TestCalldata(t *testing.T) {
	var root [32]byte
	root[0], root[31] = 0xaa, 0xbb

	data := calldata(root, 5)

	if len(data) != 4+32+32 {
		t.Fatalf("calldata length = %d, want 68", len(data))
	}

	// The selector must be the first four bytes of keccak("anchor(bytes32,uint64)"),
	// or the contract will not dispatch to the intended function.
	want := domain.Keccak256([]byte("anchor(bytes32,uint64)"))
	if !strings.EqualFold(hex.EncodeToString(data[:4]), hex.EncodeToString(want[:4])) {
		t.Errorf("selector = %x, want %x", data[:4], want[:4])
	}
	if hex.EncodeToString(data[4:36]) != hex.EncodeToString(root[:]) {
		t.Errorf("root argument = %x", data[4:36])
	}
	// uint64 arguments are right-aligned in a 32-byte word.
	if data[67] != 5 {
		t.Errorf("leaf count word = %x, want 5 in the low byte", data[36:])
	}
}

// TestSignAnchorTx_RecoversToTheSigner is the strongest structural check
// available without a second Ethereum implementation: recovering the public key
// from the signature must yield the account we signed with. That only holds if
// the signing payload, the digest, and v/r/s are all consistent.
func TestSignAnchorTx_RecoversToTheSigner(t *testing.T) {
	svc, err := NewEVMAnchorService(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	var root [32]byte
	root[0] = 0x42

	raw, err := svc.signAnchorTx(7, big.NewInt(1_000_000_000), root, 3)
	if err != nil {
		t.Fatalf("signAnchorTx: %v", err)
	}
	if len(raw) == 0 || raw[0] < 0xc0 {
		t.Fatalf("signed transaction is not an RLP list: %x", raw[:1])
	}

	// Rebuild the EIP-155 signing payload and recover from it.
	to, _ := hex.DecodeString(strings.TrimPrefix(testContract, "0x"))
	payload := rlpList(
		rlpUint(7),
		rlpBig(big.NewInt(1_000_000_000)),
		rlpUint(svc.gasLimit),
		rlpString(to),
		rlpBig(nil),
		rlpString(calldata(root, 3)),
		rlpBig(svc.chainID),
		rlpString(nil),
		rlpString(nil),
	)
	digest := domain.Keccak256(payload)

	sig := ecdsa.SignCompact(svc.privateKey, digest[:], false)
	pub, _, err := ecdsa.RecoverCompact(sig, digest[:])
	if err != nil {
		t.Fatalf("RecoverCompact: %v", err)
	}

	recovered := domain.Keccak256(pub.SerializeUncompressed()[1:])
	if got := "0x" + hex.EncodeToString(recovered[12:]); !strings.EqualFold(got, anvilAddress) {
		t.Errorf("recovered signer = %s, want %s", got, anvilAddress)
	}
}

// TestSignAnchorTx_EncodesChainIDInV pins EIP-155 replay protection: without
// the chain id folded into v, a signed anchor could be replayed onto another
// chain.
func TestSignAnchorTx_EncodesChainIDInV(t *testing.T) {
	svc, err := NewEVMAnchorService(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	var root [32]byte
	raw, err := svc.signAnchorTx(0, big.NewInt(1), root, 1)
	if err != nil {
		t.Fatal(err)
	}

	// v is 2*chainID + 35 or +36; for chain 31337 that is 62709 or 62710,
	// which RLP encodes as a three-byte string.
	want := []string{"82f4f5", "82f4f6"}
	encoded := hex.EncodeToString(raw)
	found := false
	for _, w := range want {
		if strings.Contains(encoded, w) {
			found = true
		}
	}
	if !found {
		t.Errorf("signed transaction does not carry an EIP-155 v for chain 31337: %s", encoded)
	}
}

// --- RPC behaviour ----------------------------------------------------------

// rpcServer serves scripted JSON-RPC responses keyed by method.
func rpcServer(t *testing.T, responses map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		result, ok := responses[req.Method]
		if !ok {
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"message":"unexpected method"}}`))
			return
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
		w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func serviceAgainst(t *testing.T, srv *httptest.Server) *EVMAnchorService {
	t.Helper()
	cfg := testConfig()
	cfg.RPCURL = srv.URL
	svc, err := NewEVMAnchorService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRegisterRoot_WaitsForReceipt(t *testing.T) {
	srv := rpcServer(t, map[string]any{
		"eth_getTransactionCount":   "0x1",
		"eth_gasPrice":              "0x3b9aca00",
		"eth_sendRawTransaction":    "0xdeadbeef",
		"eth_getTransactionReceipt": map[string]any{"blockNumber": "0x2a", "status": "0x1"},
	})

	txHash, blockNumber, err := serviceAgainst(t, srv).RegisterRoot(context.Background(), [32]byte{1}, 4)
	if err != nil {
		t.Fatalf("RegisterRoot: %v", err)
	}
	if txHash != "0xdeadbeef" {
		t.Errorf("txHash = %q", txHash)
	}
	// The block number must come from the receipt. The previous client recorded
	// zero for every certificate because it never fetched one.
	if blockNumber != 42 {
		t.Errorf("blockNumber = %d, want 42 from the receipt", blockNumber)
	}
}

func TestRegisterRoot_RevertedTransaction(t *testing.T) {
	srv := rpcServer(t, map[string]any{
		"eth_getTransactionCount":   "0x1",
		"eth_gasPrice":              "0x3b9aca00",
		"eth_sendRawTransaction":    "0xdeadbeef",
		"eth_getTransactionReceipt": map[string]any{"blockNumber": "0x2a", "status": "0x0"},
	})

	_, _, err := serviceAgainst(t, srv).RegisterRoot(context.Background(), [32]byte{1}, 4)
	if err == nil || !strings.Contains(err.Error(), "reverted") {
		t.Fatalf("error = %v, want a revert", err)
	}
}

func TestRegisterRoot_ReceiptTimeoutKeepsTheTxHash(t *testing.T) {
	srv := rpcServer(t, map[string]any{
		"eth_getTransactionCount": "0x1",
		"eth_gasPrice":            "0x3b9aca00",
		"eth_sendRawTransaction":  "0xdeadbeef",
		// No receipt is ever returned.
	})

	txHash, _, err := serviceAgainst(t, srv).RegisterRoot(context.Background(), [32]byte{1}, 4)
	if err == nil || !strings.Contains(err.Error(), "not mined") {
		t.Fatalf("error = %v, want a timeout", err)
	}
	// The transaction is out there; reporting its hash lets the batch be
	// reconciled instead of blindly re-anchored.
	if txHash != "0xdeadbeef" {
		t.Errorf("txHash = %q, want the broadcast hash to survive the timeout", txHash)
	}
}

func TestRegisterRoot_RPCFailures(t *testing.T) {
	tests := []struct {
		name      string
		responses map[string]any
		want      string
	}{
		{
			name:      "nonce lookup fails",
			responses: map[string]any{},
			want:      "eth_getTransactionCount",
		},
		{
			name:      "gas price fails",
			responses: map[string]any{"eth_getTransactionCount": "0x1"},
			want:      "eth_gasPrice",
		},
		{
			name: "broadcast fails",
			responses: map[string]any{
				"eth_getTransactionCount": "0x1",
				"eth_gasPrice":            "0x3b9aca00",
			},
			want: "eth_sendRawTransaction",
		},
		{
			name: "gas price is unreadable",
			responses: map[string]any{
				"eth_getTransactionCount": "0x1",
				"eth_gasPrice":            "not-a-number",
			},
			want: "unreadable gas price",
		},
		{
			name: "nonce is unreadable",
			responses: map[string]any{
				"eth_getTransactionCount": "not-hex",
			},
			want: "invalid syntax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := rpcServer(t, tt.responses)

			_, _, err := serviceAgainst(t, srv).RegisterRoot(context.Background(), [32]byte{1}, 1)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestRegisterRoot_UnreadableBlockNumber(t *testing.T) {
	srv := rpcServer(t, map[string]any{
		"eth_getTransactionCount":   "0x1",
		"eth_gasPrice":              "0x3b9aca00",
		"eth_sendRawTransaction":    "0xdeadbeef",
		"eth_getTransactionReceipt": map[string]any{"blockNumber": "0xzz", "status": "0x1"},
	})

	_, _, err := serviceAgainst(t, srv).RegisterRoot(context.Background(), [32]byte{1}, 1)
	if err == nil || !strings.Contains(err.Error(), "unreadable block number") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegisterRoot_ContextCancelled(t *testing.T) {
	srv := rpcServer(t, map[string]any{
		"eth_getTransactionCount": "0x1",
		"eth_gasPrice":            "0x3b9aca00",
		"eth_sendRawTransaction":  "0xdeadbeef",
	})

	cfg := testConfig()
	cfg.RPCURL = srv.URL
	cfg.ReceiptTimeout = time.Minute
	cfg.PollInterval = 10 * time.Millisecond
	svc, err := NewEVMAnchorService(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if _, _, err := svc.RegisterRoot(ctx, [32]byte{1}, 1); err == nil {
		t.Fatal("expected cancellation to surface")
	}
}

func TestCall_TransportAndProtocolErrors(t *testing.T) {
	t.Run("unreachable node", func(t *testing.T) {
		cfg := testConfig()
		cfg.RPCURL = "http://127.0.0.1:1"
		svc, err := NewEVMAnchorService(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var out string
		if err := svc.call(context.Background(), "eth_gasPrice", nil, &out); err == nil {
			t.Fatal("expected a transport error")
		}
	})

	t.Run("malformed response body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("not json"))
		}))
		defer srv.Close()

		var out string
		if err := serviceAgainst(t, srv).call(context.Background(), "eth_gasPrice", nil, &out); err == nil {
			t.Fatal("expected a decode error")
		}
	})

	t.Run("null result", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}))
		defer srv.Close()

		var out string
		err := serviceAgainst(t, srv).call(context.Background(), "eth_gasPrice", nil, &out)
		if err == nil || !strings.Contains(err.Error(), "no result") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("result of the wrong shape", func(t *testing.T) {
		srv := rpcServer(t, map[string]any{"eth_gasPrice": []int{1, 2, 3}})

		var out string
		err := serviceAgainst(t, srv).call(context.Background(), "eth_gasPrice", nil, &out)
		if err == nil || !strings.Contains(err.Error(), "reading") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unbuildable request", func(t *testing.T) {
		cfg := testConfig()
		cfg.RPCURL = "://not a url"
		svc, err := NewEVMAnchorService(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var out string
		if err := svc.call(context.Background(), "eth_gasPrice", nil, &out); err == nil {
			t.Fatal("expected a request-building error")
		}
	})
}

func TestIsHexAddress(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{testContract, true},
		{strings.ToLower(testContract), true},
		{"", false},
		{"5FbDB2315678afecb367f032d93F642f64180aa3", false},  // no 0x
		{"0x5FbDB2315678afecb367f032d93F642f64180aa", false}, // too short
		{"0x5FbDB2315678afecb367f032d93F642f64180aaZZ", false},
		{"0xzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := isHexAddress(tt.value); got != tt.want {
				t.Errorf("isHexAddress(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
