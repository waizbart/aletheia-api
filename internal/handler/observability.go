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

// ThumbnailProvider renders a small preview image for a certified content
// hash. Backed by the thumbnail use case, which reconstructs it from the
// certificate's stored color grid — no image blobs are stored anywhere.
type ThumbnailProvider interface {
	Execute(ctx context.Context, contentHash string) ([]byte, error)
}

// thumbHashPattern restricts servable thumbnails to content-addressed hashes
// (64-hex sha256). This blocks arbitrary-key probing through the public
// dashboard endpoint.
var thumbHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// RegisterObservabilityRoutes mounts the live pipeline dashboard, its SSE event
// stream, the recent-trace history API and the candidate-thumbnail renderer,
// all under /observability.
func RegisterObservabilityRoutes(mux *http.ServeMux, c *observability.Collector, thumbs ThumbnailProvider) {
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

	mux.HandleFunc("GET /observability/thumb/{hash}", func(w http.ResponseWriter, r *http.Request) {
		serveThumbnail(w, r, thumbs)
	})
}

// serveThumbnail renders a candidate preview from the certificate's stored
// color grid. Hashes are validated against the content-addressed pattern; the
// grid is immutable for a given content hash so responses cache aggressively.
func serveThumbnail(w http.ResponseWriter, r *http.Request, thumbs ThumbnailProvider) {
	hash := r.PathValue("hash")
	if !thumbHashPattern.MatchString(hash) {
		writeError(w, http.StatusBadRequest, "invalid content hash")
		return
	}
	data, err := thumbs.Execute(r.Context(), hash)
	if err != nil {
		writeError(w, http.StatusNotFound, "thumbnail not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
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
