// Package webassets exposes the same bundled viewer used by fleet workspaces.
package webassets

import "embed"

//go:embed novnc.js workspace-desktop.js xterm.js xterm-fit.js xterm.css workspace-terminal.js
var Desktop embed.FS

// Standalone uses the controller's visual language without its fleet navigation
// or controller API client. Keeping these assets shared prevents style drift.
//
//go:embed vbox-tokens.css fonts.css chat.css vbox-c.css inter-latin-wght-normal.woff2 vbox-logo.png vbox-logo-dark.png
var ChatStyle embed.FS

// Shared controls and safe Markdown rendering for hub and standalone views.
//
//go:embed model-picker.js markdown.js chat.html
var ChatControls embed.FS
