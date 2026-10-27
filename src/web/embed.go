package web

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var assets embed.FS

// root is resolved once at package init: if dist/ is missing or empty, the Go
// binary cannot serve the SPA and there is no safe fallback. Failing loudly at
// startup is preferable to serving an opaque 404 from http.FileServer later.
var root fs.FS

func init() {
	sub, e := fs.Sub(assets, "dist")
	if e != nil {
		log.Fatalf("web: embedded dist/ unavailable: %v", e)
	}
	entries, e := fs.ReadDir(sub, ".")
	if e != nil {
		log.Fatalf("web: cannot read embedded dist/: %v", e)
	}
	if len(entries) == 0 {
		log.Fatalf("web: embedded dist/ is empty; rebuild via `make web-build` before compiling")
	}
	root = sub
}

func Handler() http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "." || clean == "" {
			clean = "index.html"
		}
		info, e := fs.Stat(root, clean)
		if e != nil || info.IsDir() {
			// Directory listing is disabled: leaked asset names (even hashed ones)
			// expose the build manifest and seed path traversal probes. Only serve
			// concrete files; missing paths fall through to SPA index for non-API
			// routes so client-side navigation can resolve deep links.
			if clean != "index.html" && !strings.HasPrefix(clean, "assets/") {
				clean = "index.html"
				if info, e = fs.Stat(root, clean); e != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Cache-Control", "no-store")
			} else {
				http.NotFound(w, r)
				return
			}
		}
		// Hashed vite assets (assets/index-*.js, *.css, fonts) carry content
		// hashes in their filenames; a stale cached copy is byte-identical to
		// the latest. index.html itself stays no-store so policy/UI changes are
		// never masked by an intermediate proxy cache. The header set in
		// httpapi/server.go defaulted to no-store, so only override for actual
		// asset files confirmed to exist above.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
