// SPDX-License-Identifier: GPL-3.0-only

// Package ui serves the embedded admin web UI and opens it in an Edge app window.
package ui

import (
	"net/http"
	"strings"

	"datacodex-notify/web"
)

const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Handler serves the embedded web assets with restrictive security headers.
func Handler() http.Handler {
	files := http.FileServer(http.FS(web.FS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			h.Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}
