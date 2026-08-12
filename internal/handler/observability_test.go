package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/observability"
)

// fakeThumbnails serves fixed PNG payloads keyed by content hash.
type fakeThumbnails map[string][]byte

func (f fakeThumbnails) Execute(_ context.Context, hash string) ([]byte, error) {
	data, ok := f[hash]
	if !ok {
		return nil, fmt.Errorf("not found: %s", hash)
	}
	return data, nil
}

// drive runs a minimal trace through a collector via its public Factory path.
func drive(c *observability.Collector) {
	f := observability.NewFactory(c, nil)
	_, rec, end := f.NewRequest(context.Background(), "test")
	rec.SetPipeline("certify")
	h := rec.StartStage(context.Background(), "sha256")
	h.SetAttrs(observability.Attr{Key: "content_hash", Value: "deadbeef"})
	h.End()
	rec.SetVerdict(observability.Verdict{Outcome: "certified"})
	end()
}

func newObsServer(c *observability.Collector) *httptest.Server {
	return newObsServerWithThumbs(c, fakeThumbnails{})
}

func newObsServerWithThumbs(c *observability.Collector, thumbs ThumbnailProvider) *httptest.Server {
	mux := http.NewServeMux()
	RegisterObservabilityRoutes(mux, c, thumbs, nil)
	return httptest.NewServer(mux)
}

func TestObservability_TracesHistory(t *testing.T) {
	c := observability.NewCollector(10)
	drive(c)

	srv := newObsServer(c)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/observability/traces")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var traces []observability.Trace
	if err := json.NewDecoder(res.Body).Decode(&traces); err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].Verdict.Outcome != "certified" {
		t.Fatalf("unexpected history: %+v", traces)
	}

	// Detail endpoint.
	id := traces[0].ID
	dres, err := http.Get(srv.URL + "/observability/traces/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d", dres.StatusCode)
	}

	// Unknown id → 404.
	nres, err := http.Get(srv.URL + "/observability/traces/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	nres.Body.Close()
	if nres.StatusCode != http.StatusNotFound {
		t.Fatalf("missing trace status = %d, want 404", nres.StatusCode)
	}
}

func TestObservability_Thumbnail(t *testing.T) {
	validHash := strings.Repeat("a", 64)
	payload := []byte("png-bytes")
	c := observability.NewCollector(10)
	srv := newObsServerWithThumbs(c, fakeThumbnails{validHash: payload})
	defer srv.Close()

	cases := []struct {
		name, hash string
		wantStatus int
	}{
		{"valid", validHash, http.StatusOK},
		{"missing", strings.Repeat("b", 64), http.StatusNotFound},
		{"too short", "short", http.StatusBadRequest},
		{"legacy blob key format", strings.Repeat("a", 64) + ".jpg", http.StatusBadRequest},
		{"uppercase hex", strings.Repeat("A", 64), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := http.Get(srv.URL + "/observability/thumb/" + tc.hash)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusOK {
				if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
					t.Fatalf("missing immutable cache header: %q", cc)
				}
				if ct := res.Header.Get("Content-Type"); ct != "image/png" {
					t.Fatalf("content-type = %q, want image/png", ct)
				}
			}
		})
	}
}

func TestObservability_SSEStream(t *testing.T) {
	c := observability.NewCollector(10)
	srv := newObsServer(c)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/observability/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	reader := bufio.NewReader(res.Body)
	// The handler writes ": connected" immediately once the subscription is
	// registered; reading it guarantees we won't miss the events we publish next.
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("reading preamble: %v", err)
	}

	drive(c)

	// Read lines until we see the trace_end data frame or time out.
	got := make(chan string, 1)
	go func() {
		var b strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			b.WriteString(line)
			if strings.Contains(line, "trace_end") {
				got <- b.String()
				return
			}
		}
	}()

	select {
	case payload := <-got:
		if !strings.Contains(payload, "data:") || !strings.Contains(payload, "certified") {
			t.Fatalf("unexpected SSE payload: %s", payload)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE event")
	}
}
