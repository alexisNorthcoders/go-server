package handlers

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed apidocs
var apiDocs embed.FS

// APIDocsHandler serves the Swagger UI page for the Pi endpoints and the
// OpenAPI spec it reads.
func APIDocsHandler() http.Handler {
	sub, _ := fs.Sub(apiDocs, "apidocs")
	return http.StripPrefix("/api-docs", http.FileServerFS(sub))
}
