// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"context"
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// slogLogger bridges whatsmeow's logger interface to slog.
type slogLogger struct {
	module string
	min    slog.Level
}

func newSlogLogger(module string, min slog.Level) waLog.Logger {
	return &slogLogger{module: module, min: min}
}

func (l *slogLogger) log(level slog.Level, msg string, args []any) {
	if level < l.min {
		return
	}
	slog.Log(context.Background(), level, fmt.Sprintf(msg, args...), "module", l.module)
}

func (l *slogLogger) Warnf(msg string, args ...any)  { l.log(slog.LevelWarn, msg, args) }
func (l *slogLogger) Errorf(msg string, args ...any) { l.log(slog.LevelError, msg, args) }
func (l *slogLogger) Infof(msg string, args ...any)  { l.log(slog.LevelInfo, msg, args) }
func (l *slogLogger) Debugf(msg string, args ...any) { l.log(slog.LevelDebug, msg, args) }

func (l *slogLogger) Sub(module string) waLog.Logger {
	return &slogLogger{module: l.module + "/" + module, min: l.min}
}
