package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// maxJSONBody bounds JSON request bodies. Uploads have their own, larger limit;
// nothing that arrives as JSON here is anywhere near this size.
const maxJSONBody = 1 << 20 // 1 MB

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// logUsageFailure records a lost billing count. The operation it belongs to has
// already succeeded, so this must never turn into a failed response — an
// under-count is a billing problem, a failed capture is a customer problem.
func logUsageFailure(err error) {
	log.Printf("usage accounting: %v", err)
}

// isMediaError reports whether err is one of the video rejections, which carry
// their own status codes rather than collapsing into a generic failure.
func isMediaError(err error) bool {
	return errors.Is(err, domain.ErrVideoTooLarge) ||
		errors.Is(err, domain.ErrVideoTooLong) ||
		errors.Is(err, domain.ErrVideoResolution) ||
		errors.Is(err, domain.ErrVideoUndecodable)
}

// writeMediaError maps a media failure to the status a client can act on.
//
// A generic 500 would tell a caller nothing about whether to retry, shorten
// the clip or re-encode it, and these are the three things they can actually
// do about a rejected video.
func writeMediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrVideoTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "video exceeds the upload size limit")
	case errors.Is(err, domain.ErrVideoTooLong):
		writeError(w, http.StatusUnprocessableEntity, "video exceeds the duration limit")
	case errors.Is(err, domain.ErrVideoResolution):
		writeError(w, http.StatusUnprocessableEntity, "video exceeds the resolution limit")
	case errors.Is(err, domain.ErrVideoUndecodable):
		writeError(w, http.StatusUnsupportedMediaType, "video could not be decoded")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
