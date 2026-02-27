package handler

import (
	"mime/multipart"
	"net/http"
	"strings"
)

const maxUploadSize = 100 << 20 // 100 MB

var allowedMediaTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"image/bmp":       true,
	"image/tiff":      true,
	"video/mp4":       true,
	"video/webm":      true,
	"video/quicktime": true,
	"video/x-msvideo": true,
	"video/mpeg":      true,
}

func parseMediaUpload(w http.ResponseWriter, r *http.Request) (multipart.File, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or invalid file field")
		return nil, false
	}

	contentType := header.Header.Get("Content-Type")
	if !allowedMediaTypes[strings.ToLower(contentType)] {
		file.Close()
		writeError(w, http.StatusUnsupportedMediaType, "only image and video files are accepted")
		return nil, false
	}

	return file, true
}
