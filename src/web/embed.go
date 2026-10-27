package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var assets embed.FS

func Handler() http.Handler {
	root, _ := fs.Sub(assets, "dist")
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
		files.ServeHTTP(w, r)
	})
}
