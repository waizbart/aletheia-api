package handler

import (
	"encoding/json"
	"log"
	"net/http"
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
