// Package terminal hosts the embedded single-page application of the AI trading
// terminal. The Go backend (cmd/terminal, terminal/api) serves this file system;
// the SPA itself is buildless (plain ES modules, no external assets).
package terminal

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFS embed.FS

// WebFS returns the SPA root (index.html, app.js, styles.css, ...).
func WebFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// fs.Sub on a //go:embed directory cannot fail; keep the signature
		// simple for the callers.
		panic(err)
	}
	return sub
}
