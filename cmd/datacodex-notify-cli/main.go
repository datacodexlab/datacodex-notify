// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"

	"datacodex-notify/internal/cli"
)

var version = "1.0.0"

func main() {
	code := cli.Run(os.Args[1:], version)
	os.Exit(code)
}
