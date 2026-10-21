// Package web embeds the built frontend so production ships as a single binary
// (JD-009). This file lives beside dist/ because //go:embed cannot reference
// paths outside its own package directory.
package web

import (
	"embed"
	"io/fs"
)

// all: is required: embed skips dotfiles, so a dist/ holding only .gitkeep
// fails to build without it.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the build output rooted at dist/. Before the frontend is
// built it is empty but valid.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// dist is embedded at compile time; this cannot fail at runtime.
		panic(err)
	}
	return sub
}
