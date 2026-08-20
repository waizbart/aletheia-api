//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/repository"
	"github.com/waizbart/aletheia-api/internal/testdata"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

var testdataDir = testdata.Curated("aletheia")

type e2eEnv struct {
	server  *httptest.Server
	db      *sql.DB
	cleanup func()
}

// fakeChain stands in for the anchor contract: the e2e suite exercises
// certification and verification, which no longer touch the chain at all.
type fakeChain struct {
	calls int
}

func (f *fakeChain) RegisterRoot(_ context.Context, root [32]byte, _ uint64) (string, uint64, error) {
	f.calls++
	return "0xfaketx" + hex.EncodeToString(root[:4]), 1, nil
}

func setupE2E(t *testing.T) *e2eEnv {
	t.Helper()
	if os.Getenv("E2E") != "1" {
		t.Skip("set E2E=1 to run end-to-end tests (requires postgres reachable; see docker-compose.yml)")
	}

	dsn := envOr("DATABASE_URL", "postgres://aletheia:aletheia@localhost:5432/aletheia?sslmode=disable")

	ctx := context.Background()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping db: %v (is docker-compose up?)", err)
	}

	resetState(t, ctx, db)

	extractor := feature.NewOpenCVExtractor()
	certRepo := repository.NewPostgresCertificateRepo(db)

	certifyUC := usecase.NewCertifyUseCase(certRepo, extractor)
	verifyUC := usecase.NewVerifyUseCase(certRepo, extractor)
	deleteUC := usecase.NewDeleteUseCase(certRepo)
	certHandler := handler.NewCertificateHandler(certifyUC, verifyUC, deleteUC, nil, true)

	mux := http.NewServeMux()
	certHandler.RegisterRoutes(mux, nil, nil)
	server := httptest.NewServer(mux)

	cleanup := func() {
		server.Close()
		extractor.Close()
		_ = db.Close()
	}

	return &e2eEnv{
		server:  server,
		db:      db,
		cleanup: cleanup,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func resetState(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "TRUNCATE certificates CASCADE"); err != nil {
		t.Fatalf("truncate certificates: %v", err)
	}
}

func loadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return b
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type certResp struct {
	ID          string `json:"id"`
	ContentHash string `json:"content_hash"`
	Registrant  string `json:"registrant"`
	TxHash      string `json:"tx_hash"`
	BlockNumber uint64 `json:"block_number"`
	CreatedAt   string `json:"created_at"`
	// Anchor is absent until the worker commits the batch, which is what
	// distinguishes "certified" from "anchored".
	Anchor *anchorResp `json:"anchor"`
}

type anchorResp struct {
	TxHash      string   `json:"tx_hash"`
	BlockNumber uint64   `json:"block_number"`
	LeafIndex   int      `json:"leaf_index"`
	MerkleProof []string `json:"merkle_proof"`
}

type verifyResp struct {
	Certified   bool      `json:"certified"`
	Certificate *certResp `json:"certificate"`
}

type errResp struct {
	Error string `json:"error"`
}

func postCertify(t *testing.T, env *e2eEnv, filename, contentType string, body []byte, registrant string) (int, []byte) {
	t.Helper()
	req := newMultipartReq(t, http.MethodPost, env.server.URL+"/certificates", filename, contentType, body)
	if registrant != "" {
		req.Header.Set("X-Registrant", registrant)
	}
	return doRequest(t, req)
}

func postVerifyFile(t *testing.T, env *e2eEnv, filename, contentType string, body []byte) (int, []byte) {
	t.Helper()
	status, respBody, err := postVerifyFileResult(env, filename, contentType, body)
	if err != nil {
		t.Fatalf("post verify file: %v", err)
	}
	return status, respBody
}

func postVerifyFileResult(env *e2eEnv, filename, contentType string, body []byte) (int, []byte, error) {
	req, err := newMultipartReqResult(http.MethodPost, env.server.URL+"/certificates/verify", filename, contentType, body)
	if err != nil {
		return 0, nil, err
	}
	status, respBody, err := doRequestResult(req)
	if err != nil {
		return 0, nil, err
	}
	return status, respBody, nil
}

func getVerifyHash(t *testing.T, env *e2eEnv, hash string) (int, []byte) {
	t.Helper()
	url := env.server.URL + "/certificates/verify"
	if hash != "" {
		url += "?hash=" + hash
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	return doRequest(t, req)
}

func newMultipartReq(t *testing.T, method, url, filename, contentType string, body []byte) *http.Request {
	t.Helper()
	req, err := newMultipartReqResult(method, url, filename, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func newMultipartReqResult(method, url, filename, contentType string, body []byte) (*http.Request, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", contentType)
	pw, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("multipart create part: %w", err)
	}
	if _, err := pw.Write(body); err != nil {
		return nil, fmt.Errorf("multipart write: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("multipart close: %w", err)
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("new req: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req, nil
}

func doRequest(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()
	status, body, err := doRequestResult(req)
	if err != nil {
		t.Fatal(err)
	}
	return status, body
}

func doRequestResult(req *http.Request) (int, []byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, body, nil
}

func decodeCert(t *testing.T, body []byte) certResp {
	t.Helper()
	var c certResp
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode cert: %v body=%s", err, body)
	}
	return c
}

func decodeVerify(t *testing.T, body []byte) verifyResp {
	t.Helper()
	var v verifyResp
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode verify: %v body=%s", err, body)
	}
	return v
}

func assertRowExists(t *testing.T, db *sql.DB, contentHash string) (phashLen, descLen, kpLen, gridLen int) {
	t.Helper()
	row := db.QueryRow(`
		SELECT
			COALESCE(octet_length(phash), 0),
			COALESCE(octet_length(orb_descriptors), 0),
			COALESCE(octet_length(orb_keypoints), 0),
			COALESCE(octet_length(color_grid), 0)
		FROM certificates WHERE content_hash = $1`, contentHash)
	if err := row.Scan(&phashLen, &descLen, &kpLen, &gridLen); err != nil {
		t.Fatalf("query certificate row: %v", err)
	}
	return
}

func countCertificates(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM certificates").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
