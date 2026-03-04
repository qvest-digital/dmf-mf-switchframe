//go:build embed_ui

package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed ui
var uiFS embed.FS

func uiHandler() http.Handler {
	// Strip the "ui" prefix from the embedded filesystem
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err) // build-time error, should never happen
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Don't intercept API or MoQ paths
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/moq") {
			http.NotFound(w, r)
			return
		}

		// For immutable assets, set long cache headers
		if strings.HasPrefix(path, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		// Check if file exists; if not, serve index.html (SPA fallback)
		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			// SPA fallback: serve index.html
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		fileServer.ServeHTTP(w, r)
	})
}
