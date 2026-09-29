package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
	}
}

func TestForPreset(t *testing.T) {
	tests := []struct {
		preset     string
		loop       string
		tickRate   int
		transports []string
		maxPlayers int
	}{
		{PresetRealtimeAction, "tick", 60, []string{"ws", "udp"}, 16},
		{PresetTurnBased, "event", 0, []string{"ws"}, 2},
		{PresetLobbyChat, "event", 0, []string{"ws"}, 50},
		{PresetCustom, "event", 0, []string{"ws"}, 8},
	}
	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			c, err := ForPreset(tt.preset, "my-game")
			if err != nil {
				t.Fatalf("ForPreset: %v", err)
			}
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if c.Game.Preset != tt.preset || c.Game.Name != "my-game" {
				t.Errorf("game = %+v", c.Game)
			}
			if c.Engine.Loop != tt.loop || c.Engine.TickRate != tt.tickRate || c.Engine.MaxPlayersPerRoom != tt.maxPlayers {
				t.Errorf("engine = %+v", c.Engine)
			}
			if !slices.Equal(c.Server.Transports, tt.transports) {
				t.Errorf("transports = %v, want %v", c.Server.Transports, tt.transports)
			}
		})
	}

	t.Run("empty name keeps sandbox", func(t *testing.T) {
		c, err := ForPreset(PresetTurnBased, "")
		if err != nil || c.Game.Name != "bloomsom-sandbox" {
			t.Fatalf("got name %q err %v", c.Game.Name, err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if _, err := ForPreset("mmo", "x"); err == nil {
			t.Fatal("expected error for unknown preset")
		}
	})
}

func TestWriteLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFile)
	want, err := ForPreset(PresetRealtimeAction, "shooter")
	if err != nil {
		t.Fatal(err)
	}
	want.Netsim = NetsimConfig{Latency: 100 * time.Millisecond, Jitter: 20 * time.Millisecond, Loss: 0.05}

	if err := Write(path, want, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch\n got: %+v\nwant: %+v", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !regexp.MustCompile(`session_ttl: 24h`).MatchString(text) {
		t.Errorf("session_ttl not human-readable:\n%s", text)
	}
	if strings.Contains(text, "86400000000000") {
		t.Errorf("duration written as nanoseconds:\n%s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&^0o644 != 0 {
		t.Errorf("mode = %v", perm)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, found, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("cfg = %+v, want Default()", cfg)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	t.Run("no file", func(t *testing.T) {
		t.Setenv("BLOOMSOM_SERVER_WS_PORT", "9000")
		cfg, found, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
		if err != nil || found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		if cfg.Server.WSPort != 9000 {
			t.Errorf("WSPort = %d", cfg.Server.WSPort)
		}
	})
	t.Run("env beats file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), DefaultFile)
		c := Default()
		c.Server.WSPort = 8000
		if err := Write(path, c, false); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BLOOMSOM_SERVER_WS_PORT", "9000")
		cfg, found, err := Load(path)
		if err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		if cfg.Server.WSPort != 9000 {
			t.Errorf("WSPort = %d", cfg.Server.WSPort)
		}
	})
	t.Run("transports and duration", func(t *testing.T) {
		for _, v := range []string{"ws,udp", "ws udp", "ws, udp"} {
			t.Setenv("BLOOMSOM_SERVER_TRANSPORTS", v)
			t.Setenv("BLOOMSOM_AUTH_SESSION_TTL", "2h")
			cfg, _, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
			if err != nil {
				t.Fatalf("%q: %v", v, err)
			}
			if !slices.Equal(cfg.Server.Transports, []string{"ws", "udp"}) {
				t.Errorf("%q: transports = %v", v, cfg.Server.Transports)
			}
			if cfg.Auth.SessionTTL != 2*time.Hour {
				t.Errorf("session_ttl = %v", cfg.Auth.SessionTTL)
			}
		}
	})
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		key    string
	}{
		{"tick without rate", func(c *Config) { c.Engine.Loop = "tick"; c.Engine.TickRate = 0 }, "engine.tick_rate"},
		{"event with rate", func(c *Config) { c.Engine.TickRate = 30 }, "engine.tick_rate"},
		{"duplicate transports", func(c *Config) { c.Server.Transports = []string{"ws", "ws"} }, "server.transports"},
		{"unknown transport", func(c *Config) { c.Server.Transports = []string{"tcp"} }, "server.transports"},
		{"port clash", func(c *Config) {
			c.Server.Transports = []string{"ws", "udp"}
			c.Server.UDPPort = c.Server.WSPort
		}, "server.udp_port"},
		{"loss too high", func(c *Config) { c.Netsim.Loss = 1.5 }, "netsim.loss"},
		{"bad log level", func(c *Config) { c.Log.Level = "verbose" }, "log.level"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			tt.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not mention %s", err, tt.key)
			}
		})
	}

	t.Run("multiple problems", func(t *testing.T) {
		c := Default()
		c.Server.Host = ""
		c.Engine.MaxRooms = 0
		c.Log.Format = "xml"
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		for _, key := range []string{"server.host", "engine.max_rooms", "log.format"} {
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not mention %s", err, key)
			}
		}
	})
}

func TestLoadInvalid(t *testing.T) {
	dir := t.TempDir()
	tests := map[string]string{
		"bad yaml":     "game: [unclosed\n  name: x",
		"fails checks": "engine:\n  loop: tick\n  tick_rate: 0\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(path); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestWriteNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFile)
	if err := Write(path, Default(), false); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, Default(), false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want os.ErrExist", err)
	}
	if err := Write(path, Default(), true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
}

func TestIsLAN(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1":   false,
		"localhost":   false,
		"::1":         false,
		"0.0.0.0":     true,
		"192.168.1.5": true,
	}
	for host, want := range tests {
		c := Default()
		c.Server.Host = host
		if got := c.IsLAN(); got != want {
			t.Errorf("IsLAN(%q) = %v, want %v", host, got, want)
		}
	}
}
