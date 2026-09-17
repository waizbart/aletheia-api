package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/waizbart/aletheia-api/internal/domain"
)

const (
	// maxImageUploadSize is unchanged. An image is decoded whole into memory,
	// and MAX_CONCURRENT_REQUESTS is what bounds the total.
	maxImageUploadSize = 100 << 20 // 100 MB

	// maxVideoUploadSize covers roughly a minute of 4K or ten of 1080p.
	// WhatsApp caps sends at 16 MB; a phone shooting 4K/60 produces about
	// 400 MB a minute, so this is the useful middle.
	maxVideoUploadSize = 256 << 20 // 256 MB

	// maxAnonymousVideoUploadSize is the ceiling for a caller presenting no API
	// key. Verification is the free half of the product and stays free, but a
	// 256 MB decode per unauthenticated request is a denial of service offered
	// to the internet. Verifying by hash is unmetered and unrestricted, and it
	// is the cheap path.
	maxAnonymousVideoUploadSize = 32 << 20 // 32 MB

	// sniffLen is what http.DetectContentType reads.
	sniffLen = 512
)

var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
	"image/tiff": true,
}

var allowedVideoTypes = map[string]bool{
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-matroska": true,
	"video/x-msvideo":  true,
	"video/avi":        true,
	"video/mpeg":       true,
	"video/3gpp":       true,
}

// uploadLimits are the per-request ceilings. They differ by route: the legacy
// image path takes no video at all, and an unauthenticated verification takes
// a smaller one than an authenticated request.
type uploadLimits struct {
	maxImageBytes int64
	// maxVideoBytes of zero or less means this route does not accept video.
	maxVideoBytes int64
}

func imageOnlyLimits() uploadLimits {
	return uploadLimits{maxImageBytes: maxImageUploadSize}
}

func attestedLimits() uploadLimits {
	return uploadLimits{maxImageBytes: maxImageUploadSize, maxVideoBytes: maxVideoUploadSize}
}

// verifyLimits tiers video by whether the caller authenticated.
func verifyLimits(authenticated bool) uploadLimits {
	video := int64(maxAnonymousVideoUploadSize)
	if authenticated {
		video = maxVideoUploadSize
	}
	return uploadLimits{maxImageBytes: maxImageUploadSize, maxVideoBytes: video}
}

// parseMediaUpload reads the uploaded file and reports which pipeline should
// handle it.
//
// The declared part content type selects the pipeline. For video it is then
// checked against the bytes, because a video goes into libavcodec — a large C
// surface fed untrusted input — and "declared video, actually something else"
// is the cheapest way to probe it. Images are not sniffed: they already pass
// through a decoder that validates them, so the guard would only restate what
// the existing pipeline already enforces.
func parseMediaUpload(w http.ResponseWriter, r *http.Request, limits uploadLimits) (multipart.File, domain.MediaKind, bool) {
	ceiling := limits.maxImageBytes
	if limits.maxVideoBytes > ceiling {
		ceiling = limits.maxVideoBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, ceiling)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or invalid file field")
		return nil, "", false
	}

	declared := strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))

	kind := domain.MediaKindImage
	limit := limits.maxImageBytes
	switch {
	case allowedImageTypes[declared]:
	case allowedVideoTypes[declared]:
		if limits.maxVideoBytes <= 0 {
			file.Close()
			writeError(w, http.StatusUnsupportedMediaType, "this endpoint accepts image files only")
			return nil, "", false
		}
		kind, limit = domain.MediaKindVideo, limits.maxVideoBytes
	default:
		file.Close()
		writeError(w, http.StatusUnsupportedMediaType, "only image and video files are accepted")
		return nil, "", false
	}

	// The outer MaxBytesReader is set to the larger of the two ceilings so a
	// video can arrive at all, so the per-kind ceiling has to be enforced here.
	if limit > 0 && header.Size > limit {
		file.Close()
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds the upload size limit")
		return nil, "", false
	}

	if kind == domain.MediaKindVideo && !looksLikeVideo(file) {
		file.Close()
		writeError(w, http.StatusUnsupportedMediaType,
			"the uploaded bytes are not a recognised video container")
		return nil, "", false
	}

	return file, kind, true
}

// looksLikeVideo checks the leading bytes against a known container signature.
//
// multipart.File is an io.ReaderAt, so the read happens at an explicit offset
// and leaves the file position alone — nothing has to be rewound afterwards,
// and nothing is buffered.
func looksLikeVideo(file multipart.File) bool {
	head := make([]byte, sniffLen)
	n, err := file.ReadAt(head, 0)
	if n == 0 && err != nil {
		return false
	}
	head = head[:n]

	if strings.HasPrefix(strings.ToLower(http.DetectContentType(head)), "video/") {
		return true
	}
	// Go's sniff table maps only a few ISO base media brands, so QuickTime and
	// several MP4 brands come back as application/octet-stream. Matching the
	// ftyp box directly covers them without loosening the check to "anything
	// unrecognised".
	return len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp"))
}
