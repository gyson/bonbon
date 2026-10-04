package webui

import _ "embed"

// MenuBarIcon is rendered from web/src/bonbon.svg during the frontend build.
// It has a transparent background for native macOS template coloring.
//
//go:embed dist/assets/menubar.png
var MenuBarIcon []byte
