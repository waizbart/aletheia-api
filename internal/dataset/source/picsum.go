package source

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	picsumListURL = "https://picsum.photos/v2/list?page=1&limit=1000"
	picsumImgURL  = "https://picsum.photos/id/%s/800/600"
	picsumAttrib  = "Lorem Picsum (https://picsum.photos) — Unsplash License"
)

// Picsum is a Source backed by Lorem Picsum (https://picsum.photos).
// Images are identified by stable numeric IDs and are downloaded at 800×600.
// Downloads are cached in CacheDir under <id>.jpg to allow resumable runs.
type Picsum struct {
	CacheDir string // directory for cached images (will be created if absent)
	client   *http.Client
}

func NewPicsum(cacheDir string) *Picsum {
	return &Picsum{
		CacheDir: cacheDir,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Attribution returns the attribution string for the Picsum source.
func (p *Picsum) Attribution() string { return picsumAttrib }

type picsumEntry struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	URL    string `json:"url"`
}

func (p *Picsum) List(seed int64, n int) ([]Ref, error) {
	all, err := p.fetchList()
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	refs := make([]Ref, len(all))
	for i, e := range all {
		refs[i] = Ref{
			ID:   "picsum_" + e.ID,
			URL:  fmt.Sprintf(picsumImgURL, e.ID),
			MIME: "image/jpeg",
		}
	}
	return refs, nil
}

func (p *Picsum) Fetch(ref Ref) ([]byte, error) {
	if err := os.MkdirAll(p.CacheDir, 0755); err != nil {
		return nil, fmt.Errorf("picsum: mkdir cache: %w", err)
	}
	cachePath := filepath.Join(p.CacheDir, ref.ID+".jpg")
	if b, err := os.ReadFile(cachePath); err == nil {
		return b, nil
	}
	b, err := p.download(ref.URL)
	if err != nil {
		return nil, err
	}
	if werr := os.WriteFile(cachePath, b, 0644); werr != nil {
		return nil, fmt.Errorf("picsum: write cache %q: %w", cachePath, werr)
	}
	return b, nil
}

func (p *Picsum) download(url string) ([]byte, error) {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		resp, err := p.client.Get(url) //nolint:noctx
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("picsum: download %s after %d attempts: %w", url, maxRetries, lastErr)
}

func (p *Picsum) fetchList() ([]picsumEntry, error) {
	// Try to load from a local lock file first for full reproducibility.
	lockPath := filepath.Join(p.CacheDir, "source.lock.json")
	if b, err := os.ReadFile(lockPath); err == nil {
		var entries []picsumEntry
		if json.Unmarshal(b, &entries) == nil && len(entries) > 0 {
			return entries, nil
		}
	}

	resp, err := p.client.Get(picsumListURL) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("picsum: fetch list: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("picsum: read list: %w", err)
	}
	var entries []picsumEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("picsum: parse list: %w", err)
	}

	// Validate IDs are numeric (defensive).
	valid := entries[:0]
	for _, e := range entries {
		if _, err := strconv.Atoi(e.ID); err == nil {
			valid = append(valid, e)
		}
	}
	entries = valid

	// Persist lock file.
	if err := os.MkdirAll(p.CacheDir, 0755); err == nil {
		if b, err := json.MarshalIndent(entries, "", "  "); err == nil {
			_ = os.WriteFile(lockPath, b, 0644)
		}
	}
	return entries, nil
}
