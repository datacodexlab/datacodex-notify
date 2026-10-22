// SPDX-License-Identifier: GPL-3.0-only

package app

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	logFileName    = "datacodex-notify.log"
	maxLogFileSize = 5 << 20
)

// setupLogging sends slog output to a file in logsDir. The previous file is
// kept as a single ".1" backup once it grows past maxLogFileSize.
func setupLogging(logsDir string) (io.Closer, error) {
	path := filepath.Join(logsDir, logFileName)
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogFileSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("failed to rotate log file: %w", err)
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return file, nil
}
