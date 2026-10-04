// Package webui embeds the browser client so deployment needs only the Go binary.
package webui

import (
	"embed"
	"net/http"
)

//go:embed dist
var files embed.FS

func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, files, "dist/index.html")
	})
	mux.HandleFunc("GET /assets/{path...}", func(w http.ResponseWriter, r *http.Request) {
		name := "dist/assets/" + r.PathValue("path")
		file, err := files.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, files, name)
	})
}
