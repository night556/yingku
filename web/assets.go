package web

import "embed"

// Files contains the built-in web interface.
//
//go:embed *.html *.css *.js *.svg
var Files embed.FS
