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

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/repository"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

const testdataDir = "../../lab/hashing/testdata"

type e2eEnv struct {
	server   *httptest.Server
	db       *sql.DB
	s3       *s3.Client
	bucket   string
	cleanup  func()
}

type fakeChain struct {
	calls int
}

func (f *fakeChain) RegisterHash(_ context.Context, contentHash, _ string) (string, uint64, error) {
	f.calls++
	return "0xfaketx" + contentHash[:8], 1, nil
}

func (f *fakeChain) IsHashRegistered(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func setupE2E(t *testing.T) *e2eEnv {
	t.Helper()
	if os.Getenv("E2E") != "1" {
		t.Skip("set E2E=1 to run end-to-end tests (requires postgres + minio reachable; see docker-compose.yml)")
	}

	dsn := envOr("DATABASE_URL", "postgres://aletheia:aletheia@localhost:5432/aletheia?sslmode=disable")
	endpoint := envOr("S3_ENDPOINT", "http://localhost:9000")
	bucket := envOr("S3_BUCKET", "aletheia-images")
	region := envOr("S3_REGION", "us-east-1")
	accessKey := envOr("S3_ACCESS_KEY", "minioadmin")
	secretKey := envOr("S3_SECRET_KEY", "minioadmin")

	ctx := context.Background()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping db: %v (is docker-compose up?)", err)
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	if _, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		if _, cerr := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); cerr != nil {
			t.Fatalf("ensure bucket: head=%v create=%v", err, cerr)
		}
	}

	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_BUCKET", bucket)
	t.Setenv("S3_REGION", region)
	t.Setenv("S3_ACCESS_KEY", accessKey)
	t.Setenv("S3_SECRET_KEY", secretKey)
	blobStore, err := repository.NewS3BlobStoreFromEnv(ctx)
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	resetState(t, ctx, db, s3Client, bucket)

	extractor := feature.NewOpenCVExtractor()
	certRepo := repository.NewPostgresCertificateRepo(db)
	chain := &fakeChain{}

	certifyUC := usecase.NewCertifyUseCase(certRepo, chain, extractor, blobStore)
	verifyUC := usecase.NewVerifyUseCase(certRepo, extractor, blobStore)
	certHandler := handler.NewCertificateHandler(certifyUC, verifyUC)

	mux := http.NewServeMux()
	certHandler.RegisterRoutes(mux)
	server := httptest.NewServer(mux)

	cleanup := func() {
		server.Close()
		extractor.Close()
		_ = db.Close()
	}

	return &e2eEnv{
		server:  server,
		db:      db,
		s3:      s3Client,
		bucket:  bucket,
		cleanup: cleanup,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func resetState(t *testing.T, ctx context.Context, db *sql.DB, s3Client *s3.Client, bucket string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "TRUNCATE certificates CASCADE"); err != nil {
		t.Fatalf("truncate certificates: %v", err)
	}

	var token *string
	for {
		out, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: token,
		})
		if err != nil {
			t.Fatalf("list bucket: %v", err)
		}
		if len(out.Contents) == 0 {
			break
		}
		ids := make([]types.ObjectIdentifier, 0, len(out.Contents))
		for _, o := range out.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: o.Key})
		}
		_, err = s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: ids},
		})
		if err != nil {
			t.Fatalf("delete bucket objects: %v", err)
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
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
	req := newMultipartReq(t, http.MethodPost, env.server.URL+"/certificates/verify", filename, contentType, body)
	return doRequest(t, req)
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
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", contentType)
	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("multipart create part: %v", err)
	}
	if _, err := pw.Write(body); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func doRequest(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
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

func assertRowExists(t *testing.T, db *sql.DB, contentHash string) (phashLen, descLen, kpLen int, blobKey string) {
	t.Helper()
	row := db.QueryRow(`
		SELECT
			COALESCE(octet_length(phash), 0),
			COALESCE(octet_length(orb_descriptors), 0),
			COALESCE(octet_length(orb_keypoints), 0),
			COALESCE(image_blob_key, '')
		FROM certificates WHERE content_hash = $1`, contentHash)
	if err := row.Scan(&phashLen, &descLen, &kpLen, &blobKey); err != nil {
		t.Fatalf("query certificate row: %v", err)
	}
	return
}

func assertBlobExists(t *testing.T, env *e2eEnv, key string) {
	t.Helper()
	if key == "" {
		t.Fatal("empty blob key")
	}
	_, err := env.s3.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(env.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("blob %s missing: %v", key, err)
	}
}

func countCertificates(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM certificates").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
