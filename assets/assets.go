// SPDX-License-Identifier: GPL-3.0-only

// Package assets embeds the tray icons. Regenerate them with `go generate ./assets`.
package assets

import _ "embed"

//go:generate go run ../tools/genicons -out .

//go:embed icon-green.ico
var IconGreen []byte

//go:embed icon-yellow.ico
var IconYellow []byte

//go:embed icon-red.ico
var IconRed []byte
