package handler

import (
	_ "embed"
	"net/http"
)

//go:embed static/swagger-ui.html
var swaggerHTML []byte

//go:embed static/openapi.yaml
var openAPISpec []byte

func RegisterDocsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(swaggerHTML)
	})

	mux.HandleFunc("GET /docs/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(openAPISpec)
	})
}
