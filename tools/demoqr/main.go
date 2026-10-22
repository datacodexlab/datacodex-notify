// SPDX-License-Identifier: GPL-3.0-only

// Command demoqr writes a harmless dummy QR code (the text "demo") used by the
// documentation screenshots, so no real pairing code ever appears in them.
package main

import (
	"fmt"
	"os"

	"github.com/skip2/go-qrcode"
)

func main() {
	out := "demo-qr.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := qrcode.WriteFile("demo", qrcode.Medium, 448, out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
