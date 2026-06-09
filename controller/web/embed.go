package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var staticFiles embed.FS

// Handler serves the embedded SvelteKit static build.
// Falls back to index.html for SPA client-side routing.
func Handler() http.Handler {
	dist, err := fs.Sub(staticFiles, "dist")
	if err != nil {
		panic("UI not embedded: run 'make ui-build' first")
	}
	return http.FileServer(http.FS(dist))
}
