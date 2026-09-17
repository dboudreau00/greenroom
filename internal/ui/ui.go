// Package ui embeds the portal front end: one HTML shell, one stylesheet
// and one script, no build step.
package ui

import "embed"

// Files holds the static portal assets.
//
//go:embed index.html app.js style.css
var Files embed.FS
