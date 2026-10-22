// SPDX-License-Identifier: GPL-3.0-only

// Package web embeds the admin UI assets.
package web

import "embed"

// FS holds the single-page admin UI: HTML, CSS, scripts, translations, fonts, icons, and the web app manifest.
//
//go:embed index.html app.css manifest.webmanifest js i18n fonts icons
var FS embed.FS
