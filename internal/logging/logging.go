// Package logging builds the structured logger used by the Bloomsom server.
//
// The server runs in the foreground and always streams logs to stdout so
// developers can debug; optionally the same stream is also appended to a file.
//
// Level guidance:
//   - ERROR: failures that affect the server (cannot bind port, storage errors).
//   - WARN:  degraded but still running (tick overrun, slow client dropped,
//     rate-limited login, sandbox mode enabled).
//   - INFO:  lifecycle events (start/stop, connect/disconnect, login,
//     join/leave room).
//   - DEBUG: per-packet and per-tick detail.
//
// Sensitive attributes (passwords, tokens, hashes, secrets, authorization
// headers) are always replaced with "[REDACTED]" before being written.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Options configures the logger. Values come from bloomsom.yaml `log:` section.
type Options struct {
	Level  string // "debug" | "info" | "warn" | "error"; "" means info
	Format string // "text" | "json"; "" means text
	File   string // optional path; when set, logs go to BOTH out and this file
}

// redactedValue replaces the value of any sensitive attribute.
const redactedValue = "[REDACTED]"

// textTimeLayout is RFC3339 with millisecond precision.
const textTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// sensitiveKeys lists lowercased attribute keys whose values are redacted.
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"pass":          {},
	"token":         {},
	"session_token": {},
	"password_hash": {},
	"token_hash":    {},
	"secret":        {},
	"authorization": {},
}

// ParseLevel converts "debug"|"info"|"warn"|"error" (case-insensitive) to slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logging: invalid level %q (want debug, info, warn or error)", s)
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// New builds a *slog.Logger writing to out (normally os.Stdout) and, if
// opts.File != "", also to that file. The returned io.Closer closes the file
// (it is a no-op closer when no file is used); callers defer it.
// Invalid Level or Format returns an error naming the bad value.
func New(opts Options, out io.Writer) (*slog.Logger, io.Closer, error) {
	level := slog.LevelInfo
	if opts.Level != "" {
		l, err := ParseLevel(opts.Level)
		if err != nil {
			return nil, nil, err
		}
		level = l
	}

	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return nil, nil, fmt.Errorf("logging: invalid format %q (want text or json)", opts.Format)
	}

	var closer io.Closer = nopCloser{}
	w := out
	if opts.File != "" {
		if dir := filepath.Dir(opts.File); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nil, fmt.Errorf("logging: create log dir: %w", err)
			}
		}
		f, err := os.OpenFile(opts.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("logging: open log file: %w", err)
		}
		closer = f
		w = io.MultiWriter(out, f)
	}

	isText := format == "text"
	hopts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if _, ok := sensitiveKeys[strings.ToLower(a.Key)]; ok {
				return slog.String(a.Key, redactedValue)
			}
			if isText && len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				return slog.String(a.Key, a.Value.Time().Local().Format(textTimeLayout))
			}
			return a
		},
	}

	var h slog.Handler
	if isText {
		h = slog.NewTextHandler(w, hopts)
	} else {
		h = slog.NewJSONHandler(w, hopts)
	}
	return slog.New(h), closer, nil
}
