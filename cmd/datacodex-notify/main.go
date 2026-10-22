// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"datacodex-notify/internal/app"
	"datacodex-notify/internal/cli"
)

var version = "1.0.0"

func main() {
	if len(os.Args) > 1 {
		os.Exit(cli.Run(os.Args[1:], version))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := app.Run(ctx, app.Options{Version: version}); err != nil {
		fmt.Fprintln(os.Stderr, "datacodex-notify:", err)
		os.Exit(1)
	}
}
