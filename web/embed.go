// Package web holds the agent's GUI (A5): one page shell, one script and
// the shared stylesheet, embedded in the binary. No external fonts, CDNs or
// network calls: everything the browser loads comes from the agent service.
package web

import (
	"embed"
	_ "embed"
)

// Files are the GUI's assets.
//
//go:embed index.html app.js orderecho.css
var Files embed.FS

// CSS is the shared stylesheet (the certification report inlines it).
//
//go:embed orderecho.css
var CSS string
