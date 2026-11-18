// Package web holds the built frontend. Run `npm run build` in this directory to fill dist/;
// until then the server answers UI requests with a short "frontend not built" page.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built frontend, rooted at the directory that contains index.html.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub only fails for an invalid path, and "dist" is a constant.
		panic(err)
	}
	return sub
}
