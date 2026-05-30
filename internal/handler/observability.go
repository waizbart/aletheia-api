package handler

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"time"

	"github.com/waizbart/aletheia-api/internal/observability"
)

//go:embed static/observability
var obsStaticFS embed.FS

// BlobReader is the read-only slice of the image blob store the dashboard needs
// to render candidate thumbnails. Declared here so the handler stays decoupled
// from the concrete S3 implementation.
type BlobReader interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// blobKeyPattern restricts servable blob keys to the content-addressed form the
// pipeline produces (64-hex sha256 + ".jpg"). This blocks arbitrary-key reads
// through the public dashboard endpoint.
var blobKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}\.jpg$`)

// RegisterObservabilityRoutes mounts the live pipeline dashboard, its SSE event
// stream, the recent-trace history API and the candidate-thumbnail blob proxy,
// all under /observability.
func RegisterObservabilityRoutes(mux *http.ServeMux, c *observability.Collector, blobs BlobReader) {
	sub, err := fs.Sub(obsStaticFS, "static/observability")
	if err != nil {
		panic(fmt.Sprintf("observability static fs: %v", err))
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("GET /observability", func(w http.ResponseWriter, r *http.Request) {
		serveObsFile(w, sub, "index.html", "text/html; charset=utf-8")
	})
	mux.Handle("GET /observability/static/", http.StripPrefix("/observability/static/", fileServer))

	mux.HandleFunc("GET /observability/events", func(w http.ResponseWriter, r *http.Request) {
		streamEvents(w, r, c)
	})

	mux.HandleFunc("GET /observability/traces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, c.Recent())
	})

	mux.HandleFunc("GET /observability/traces/{id}", func(w http.ResponseWriter, r *http.Request) {
		t := c.Get(r.PathValue("id"))
		if t == nil {
			writeError(w, http.StatusNotFound, "trace not found")
			return
		}
		writeJSON(w, http.StatusOK, t)
	})

	mux.HandleFunc("GET /observability/blob/{key}", func(w http.ResponseWriter, r *http.Request) {
		serveBlob(w, r, blobs)
	})
}

// serveBlob streams a stored image so the dashboard can render candidate
// thumbnails. Keys are validated against the content-addressed pattern; bytes
// are immutable (keyed by content hash) so they are cached aggressively.
func serveBlob(w http.ResponseWriter, r *http.Request, blobs BlobReader) {
	key := r.PathValue("key")
	if !blobKeyPattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid blob key")
		return
	}
	data, err := blobs.Get(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusNotFound, "blob not found")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
}

func serveObsFile(w http.ResponseWriter, sub fs.FS, name, contentType string) {
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "asset not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

// streamEvents pushes live trace events to the browser via Server-Sent Events.
func streamEvents(w http.ResponseWriter, r *http.Request, c *observability.Collector) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable proxy buffering (nginx) so events arrive immediately.
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := c.Subscribe()
	defer cancel()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}
