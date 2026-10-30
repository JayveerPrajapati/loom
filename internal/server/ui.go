package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	webassets "loom/web"
)

func (s *api) ui(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/hook/") {
		http.NotFound(w, r)
		return
	}
	assets := webassets.Assets()
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(assets, name); err != nil {
		name = "index.html"
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, assets, name)
}
