package controller

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed web/*
var controllerUI embed.FS

// uiAssetTypes lists the embedded file types served by path. HTML pages keep
// their explicit routes so only intended pages are reachable.
var uiAssetTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".png":   "image/png",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/plain; charset=utf-8",
}

func uiHandler(name, contentType string, index bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if index && r.URL.Path != "/" {
			serveUIAsset(w, r)
			return
		}
		data, err := fs.ReadFile(controllerUI, "web/"+name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("load controller UI: %w", err))
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// serveUIAsset serves top-level embedded web assets that have no explicit
// route, so new stylesheets, scripts, fonts and images cannot silently 404.
func serveUIAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	contentType, ok := uiAssetTypes[path.Ext(name)]
	if !ok || name == "" || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(controllerUI, "web/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
