// Package web contains the dashboard assets embedded into the binary.
package web

import "embed"

// FS holds the HTML templates and static assets.
//
//go:embed templates static
var FS embed.FS
