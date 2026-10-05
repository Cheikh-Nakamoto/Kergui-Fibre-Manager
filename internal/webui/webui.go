// Package webui embeds and serves the static web dashboard (Milestone 3). The
// dashboard is dependency-free HTML/CSS/JS that talks to the REST API; it is
// served by `kergui serve` from these embedded files.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var assets embed.FS

// FS returns the embedded dashboard file system rooted at the asset directory.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err) // embedded path is a compile-time constant
	}
	return sub
}

// Handler serves the dashboard (index.html at "/", plus app.js and styles.css).
func Handler() http.Handler { return http.FileServerFS(FS()) }
