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
	picsumListPageURL = "https://picsum.photos/v2/list?page=%d&limit=100"
	picsumImgURL      = "https://picsum.photos/id/%s/800/600"
	picsumAttrib      = "Lorem Picsum (https://picsum.photos) — Unsplash License"
	picsumPageSize    = 100
	picsumMaxRetries  = 3
	picsumMaxImage    = 20 << 20
	picsumMaxListPage = 1 << 20
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
	return p.fetchURL(url, picsumMaxImage)
}

func (p *Picsum) fetchURL(url string, maxBytes int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < picsumMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		resp, err := p.client.Get(url) //nolint:noctx
		if err != nil {
			lastErr = err
			continue
		}
		body, err := readLimited(resp.Body, maxBytes)
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
	return nil, fmt.Errorf("picsum: fetch %s after %d attempts: %w", url, picsumMaxRetries, lastErr)
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return b, nil
}

// fetchList retrieves all available Picsum images by paginating through the
// v2/list endpoint (max 100 per page). Results are persisted in source.lock.json
// for full reproducibility on re-runs.
func (p *Picsum) fetchList() ([]picsumEntry, error) {
	lockPath := filepath.Join(p.CacheDir, "source.lock.json")
	if b, err := os.ReadFile(lockPath); err == nil {
		var entries []picsumEntry
		if json.Unmarshal(b, &entries) == nil && len(entries) > 0 {
			return entries, nil
		}
	}

	var all []picsumEntry
	for page := 1; ; page++ {
		url := fmt.Sprintf(picsumListPageURL, page)
		body, err := p.fetchURL(url, picsumMaxListPage)
		if err != nil {
			return nil, fmt.Errorf("picsum: fetch list page %d: %w", page, err)
		}
		var page_entries []picsumEntry
		if err := json.Unmarshal(body, &page_entries); err != nil {
			return nil, fmt.Errorf("picsum: parse list page %d: %w", page, err)
		}
		// Filter to numeric IDs only.
		for _, e := range page_entries {
			if _, err := strconv.Atoi(e.ID); err == nil {
				all = append(all, e)
			}
		}
		// Picsum returns fewer than picsumPageSize entries on the last page.
		if len(page_entries) < picsumPageSize {
			break
		}
	}

	if err := os.MkdirAll(p.CacheDir, 0755); err == nil {
		if b, err := json.MarshalIndent(all, "", "  "); err == nil {
			_ = os.WriteFile(lockPath, b, 0644)
		}
	}
	return all, nil
}
