package controller

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
)

//go:embed web/*
var controllerUI embed.FS

func uiHandler(name, contentType string, index bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if index && r.URL.Path != "/" {
			http.NotFound(w, r)
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
