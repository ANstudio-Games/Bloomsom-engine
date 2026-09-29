package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"WARN", slog.LevelWarn, false},
		{"verbose", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLevel(%q) = nil error, want error", tt.in)
				}
				if !strings.Contains(err.Error(), tt.in) {
					t.Errorf("error %q does not name bad value %q", err, tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLevel(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		bad  string
	}{
		{"format", Options{Format: "xml"}, "xml"},
		{"level", Options{Level: "loud"}, "loud"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := New(tt.opts, &bytes.Buffer{})
			if err == nil {
				t.Fatal("New() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.bad) {
				t.Errorf("error %q does not name %q", err, tt.bad)
			}
		})
	}
}

func newTest(t *testing.T, opts Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, c, err := New(opts, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return l, &buf
}

func TestLevelFiltering(t *testing.T) {
	l, buf := newTest(t, Options{Level: "warn"})
	l.Info("info-msg")
	l.Warn("warn-msg")
	out := buf.String()
	if strings.Contains(out, "info-msg") {
		t.Errorf("info message logged at warn level: %q", out)
	}
	if !strings.Contains(out, "warn-msg") {
		t.Errorf("warn message missing: %q", out)
	}
}

func TestJSONFormat(t *testing.T) {
	l, buf := newTest(t, Options{Format: "json"})
	l.Info("hello", "room", "lobby")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", buf.String(), err)
	}
	if m["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", m["msg"])
	}
	if m["room"] != "lobby" {
		t.Errorf("room = %v, want lobby", m["room"])
	}
}

func TestTextTimeFormat(t *testing.T) {
	l, buf := newTest(t, Options{})
	l.Info("x")
	re := regexp.MustCompile(`^time=\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2}) `)
	if !re.MatchString(buf.String()) {
		t.Errorf("unexpected time format: %q", buf.String())
	}
}

func TestRedaction(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			tests := []struct {
				name    string
				log     func(*slog.Logger)
				secrets []string
				keep    []string
			}{
				{
					name: "args",
					log: func(l *slog.Logger) {
						l.Info("login", "username", "budi", "password", "hunter2", "token", "abc123")
					},
					secrets: []string{"hunter2", "abc123"},
					keep:    []string{"budi"},
				},
				{
					name:    "with",
					log:     func(l *slog.Logger) { l.With("session_token", "xyz").Info("x") },
					secrets: []string{"xyz"},
				},
				{
					name:    "group",
					log:     func(l *slog.Logger) { l.Info("x", slog.Group("auth", "password_hash", "h4sh")) },
					secrets: []string{"h4sh"},
				},
				{
					name:    "uppercase key",
					log:     func(l *slog.Logger) { l.Info("x", "Authorization", "Bearer s3cr3t") },
					secrets: []string{"s3cr3t"},
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					l, buf := newTest(t, Options{Format: format})
					tt.log(l)
					out := buf.String()
					if !strings.Contains(out, redactedValue) {
						t.Errorf("output missing %s: %q", redactedValue, out)
					}
					for _, s := range tt.secrets {
						if strings.Contains(out, s) {
							t.Errorf("output leaks %q: %q", s, out)
						}
					}
					for _, k := range tt.keep {
						if !strings.Contains(out, k) {
							t.Errorf("output missing %q: %q", k, out)
						}
					}
				})
			}
		})
	}
}

func TestFileOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "server.log")
	var buf bytes.Buffer
	l, c, err := New(Options{File: path}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Info("to-both")
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(buf.String(), "to-both") {
		t.Errorf("buffer missing message: %q", buf.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "to-both") {
		t.Errorf("file missing message: %q", data)
	}
}

func TestCloserWithoutFile(t *testing.T) {
	_, c, err := New(Options{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
