// Package config loads, validates and writes bloomsom.yaml, the Bloomsom
// Engine configuration file. Precedence is env (BLOOMSOM_*) > file > Default().
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// Preset names.
const (
	PresetRealtimeAction = "realtime-action"
	PresetTurnBased      = "turn-based"
	PresetLobbyChat      = "lobby-chat"
	PresetCustom         = "custom"
)

// Presets lists all valid preset names in display order.
var Presets = []string{PresetRealtimeAction, PresetTurnBased, PresetLobbyChat, PresetCustom}

// DefaultFile is the config file name looked up in the working directory.
const DefaultFile = "bloomsom.yaml"

// envPrefix is the environment variable prefix (BLOOMSOM_SERVER_WS_PORT, ...).
const envPrefix = "BLOOMSOM"

// Config is the full bloomsom.yaml document.
type Config struct {
	Game     GameConfig     `mapstructure:"game"     yaml:"game"`
	Server   ServerConfig   `mapstructure:"server"   yaml:"server"`
	Engine   EngineConfig   `mapstructure:"engine"   yaml:"engine"`
	Database DatabaseConfig `mapstructure:"database" yaml:"database"`
	Auth     AuthConfig     `mapstructure:"auth"     yaml:"auth"`
	Log      LogConfig      `mapstructure:"log"      yaml:"log"`
	Netsim   NetsimConfig   `mapstructure:"netsim"   yaml:"netsim"`
}

// GameConfig identifies the game and its preset.
type GameConfig struct {
	Name            string `mapstructure:"name"             yaml:"name"`
	Preset          string `mapstructure:"preset"           yaml:"preset"`
	ProtocolVersion int    `mapstructure:"protocol_version" yaml:"protocol_version"`
}

// ServerConfig controls network listeners.
type ServerConfig struct {
	Host       string   `mapstructure:"host"       yaml:"host"`
	WSPort     int      `mapstructure:"ws_port"    yaml:"ws_port"`
	UDPPort    int      `mapstructure:"udp_port"   yaml:"udp_port"`
	Transports []string `mapstructure:"transports" yaml:"transports"`
}

// EngineConfig controls the room loop and capacity limits.
type EngineConfig struct {
	Loop              string `mapstructure:"loop"                 yaml:"loop"`
	TickRate          int    `mapstructure:"tick_rate"            yaml:"tick_rate"`
	MaxRooms          int    `mapstructure:"max_rooms"            yaml:"max_rooms"`
	MaxPlayersPerRoom int    `mapstructure:"max_players_per_room" yaml:"max_players_per_room"`
}

// DatabaseConfig points at the SQLite database file.
type DatabaseConfig struct {
	Path string `mapstructure:"path" yaml:"path"`
}

// AuthConfig controls the built-in auth system.
type AuthConfig struct {
	SessionTTL       time.Duration `mapstructure:"session_ttl"        yaml:"session_ttl"`
	AllowRegister    bool          `mapstructure:"allow_register"     yaml:"allow_register"`
	MaxLoginAttempts int           `mapstructure:"max_login_attempts" yaml:"max_login_attempts"`
}

// LogConfig controls logging output.
type LogConfig struct {
	Level  string `mapstructure:"level"  yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
	File   string `mapstructure:"file"   yaml:"file"`
}

// NetsimConfig simulates a bad network (latency, jitter, packet loss).
type NetsimConfig struct {
	Latency time.Duration `mapstructure:"latency" yaml:"latency"`
	Jitter  time.Duration `mapstructure:"jitter"  yaml:"jitter"`
	Loss    float64       `mapstructure:"loss"    yaml:"loss"`
}

// Default returns the sandbox config used when no bloomsom.yaml exists.
func Default() Config {
	return Config{
		Game: GameConfig{Name: "bloomsom-sandbox", Preset: PresetLobbyChat, ProtocolVersion: 1},
		Server: ServerConfig{
			Host:       "127.0.0.1",
			WSPort:     7777,
			UDPPort:    7778,
			Transports: []string{"ws"},
		},
		Engine:   EngineConfig{Loop: "event", TickRate: 0, MaxRooms: 100, MaxPlayersPerRoom: 8},
		Database: DatabaseConfig{Path: "./bloomsom.db"},
		Auth:     AuthConfig{SessionTTL: 24 * time.Hour, AllowRegister: true, MaxLoginAttempts: 5},
		Log:      LogConfig{Level: "info", Format: "text", File: ""},
		Netsim:   NetsimConfig{},
	}
}

// ForPreset returns Default() adjusted for the given preset, with game.name
// set to name (an empty name keeps the sandbox name).
func ForPreset(preset, name string) (Config, error) {
	c := Default()
	c.Game.Preset = preset
	if name != "" {
		c.Game.Name = name
	}
	switch preset {
	case PresetRealtimeAction:
		c.Engine.Loop, c.Engine.TickRate = "tick", 60
		c.Server.Transports = []string{"ws", "udp"}
		c.Engine.MaxPlayersPerRoom = 16
	case PresetTurnBased:
		c.Engine.Loop, c.Engine.TickRate = "event", 0
		c.Server.Transports = []string{"ws"}
		c.Engine.MaxPlayersPerRoom = 2
	case PresetLobbyChat:
		c.Engine.Loop, c.Engine.TickRate = "event", 0
		c.Server.Transports = []string{"ws"}
		c.Engine.MaxPlayersPerRoom = 50
	case PresetCustom:
	default:
		return Config{}, fmt.Errorf("unknown preset %q (valid: %s)", preset, strings.Join(Presets, ", "))
	}
	return c, nil
}

// Load reads configuration with precedence env > file > Default(). An empty
// path means DefaultFile. A missing file is not an error: Default() with env
// overrides is returned and found is false.
func Load(path string) (cfg Config, found bool, err error) {
	if path == "" {
		path = DefaultFile
	}

	v := viper.New()
	setDefaults(v, "", reflect.ValueOf(Default()))
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if _, statErr := os.Stat(path); statErr == nil {
		found = true
		v.SetConfigFile(path)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return Config{}, true, fmt.Errorf("read %s: %w", path, err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Config{}, false, fmt.Errorf("stat %s: %w", path, statErr)
	}

	hook := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		stringToListHook,
		mapstructure.StringToTimeDurationHookFunc(),
	))
	if err := v.Unmarshal(&cfg, hook); err != nil {
		return Config{}, found, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, found, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, found, nil
}

// setDefaults registers every leaf key of val as a viper default so that
// AutomaticEnv can override any of them.
func setDefaults(v *viper.Viper, prefix string, val reflect.Value) {
	t := val.Type()
	for i := range t.NumField() {
		key := t.Field(i).Tag.Get("mapstructure")
		if prefix != "" {
			key = prefix + "." + key
		}
		f := val.Field(i)
		if f.Kind() == reflect.Struct {
			setDefaults(v, key, f)
			continue
		}
		v.SetDefault(key, f.Interface())
	}
}

// stringToListHook splits a string on commas and/or whitespace when the
// target is []string (e.g. BLOOMSOM_SERVER_TRANSPORTS="ws,udp" or "ws udp").
func stringToListHook(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to != reflect.TypeFor[[]string]() {
		return data, nil
	}
	return strings.FieldsFunc(data.(string), func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	}), nil
}

// Validate checks every field and returns a single joined error listing all
// problems, each prefixed with its yaml key.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !slices.Contains(Presets, c.Game.Preset) {
		add("game.preset: %q is not one of %s", c.Game.Preset, strings.Join(Presets, ", "))
	}
	if c.Game.ProtocolVersion < 1 {
		add("game.protocol_version: must be >= 1")
	}

	if strings.TrimSpace(c.Server.Host) == "" {
		add("server.host: must not be empty")
	}
	if c.Server.WSPort < 1 || c.Server.WSPort > 65535 {
		add("server.ws_port: must be between 1 and 65535")
	}
	if c.Server.UDPPort < 1 || c.Server.UDPPort > 65535 {
		add("server.udp_port: must be between 1 and 65535")
	}
	if len(c.Server.Transports) == 0 {
		add("server.transports: must not be empty")
	}
	seen := map[string]bool{}
	for _, tr := range c.Server.Transports {
		if tr != "ws" && tr != "udp" {
			add("server.transports: unknown transport %q (valid: ws, udp)", tr)
		}
		if seen[tr] {
			add("server.transports: duplicate transport %q", tr)
		}
		seen[tr] = true
	}
	if seen["ws"] && seen["udp"] && c.Server.WSPort == c.Server.UDPPort {
		add("server.udp_port: must differ from server.ws_port when both ws and udp are enabled")
	}

	switch c.Engine.Loop {
	case "tick":
		if c.Engine.TickRate < 1 || c.Engine.TickRate > 240 {
			add(`engine.tick_rate: must be between 1 and 240 when engine.loop is "tick"`)
		}
	case "event":
		if c.Engine.TickRate != 0 {
			add(`engine.tick_rate: must be 0 when engine.loop is "event"`)
		}
	default:
		add("engine.loop: %q is not one of event, tick", c.Engine.Loop)
	}
	if c.Engine.MaxRooms < 1 {
		add("engine.max_rooms: must be >= 1")
	}
	if c.Engine.MaxPlayersPerRoom < 1 {
		add("engine.max_players_per_room: must be >= 1")
	}

	if strings.TrimSpace(c.Database.Path) == "" {
		add("database.path: must not be empty")
	}

	if c.Auth.SessionTTL <= 0 {
		add("auth.session_ttl: must be > 0")
	}
	if c.Auth.MaxLoginAttempts < 1 {
		add("auth.max_login_attempts: must be >= 1")
	}

	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Log.Level) {
		add("log.level: %q is not one of debug, info, warn, error", c.Log.Level)
	}
	if c.Log.Format != "text" && c.Log.Format != "json" {
		add("log.format: %q is not one of text, json", c.Log.Format)
	}

	if c.Netsim.Loss < 0 || c.Netsim.Loss > 1 {
		add("netsim.loss: must be between 0 and 1")
	}
	if c.Netsim.Latency < 0 {
		add("netsim.latency: must be >= 0")
	}
	if c.Netsim.Jitter < 0 {
		add("netsim.jitter: must be >= 0")
	}

	return errors.Join(errs...)
}

// IsLAN reports whether server.host exposes the server beyond loopback
// (anything other than 127.0.0.1, ::1 or localhost).
func (c Config) IsLAN() bool {
	switch strings.ToLower(strings.TrimSpace(c.Server.Host)) {
	case "127.0.0.1", "::1", "localhost":
		return false
	}
	return true
}

// yamlDoc mirrors Config with human-readable durations for Write.
type yamlDoc struct {
	Game     GameConfig     `yaml:"game"`
	Server   ServerConfig   `yaml:"server"`
	Engine   EngineConfig   `yaml:"engine"`
	Database DatabaseConfig `yaml:"database"`
	Auth     struct {
		SessionTTL       string `yaml:"session_ttl"`
		AllowRegister    bool   `yaml:"allow_register"`
		MaxLoginAttempts int    `yaml:"max_login_attempts"`
	} `yaml:"auth"`
	Log    LogConfig `yaml:"log"`
	Netsim struct {
		Latency string  `yaml:"latency"`
		Jitter  string  `yaml:"jitter"`
		Loss    float64 `yaml:"loss"`
	} `yaml:"netsim"`
}

// Write validates c and writes it as YAML to path with mode 0o644. If the
// file exists and overwrite is false, the returned error wraps os.ErrExist.
func Write(path string, c Config, overwrite bool) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to write invalid config: %w", err)
	}

	doc := yamlDoc{Game: c.Game, Server: c.Server, Engine: c.Engine, Database: c.Database, Log: c.Log}
	doc.Auth.SessionTTL = c.Auth.SessionTTL.String()
	doc.Auth.AllowRegister = c.Auth.AllowRegister
	doc.Auth.MaxLoginAttempts = c.Auth.MaxLoginAttempts
	doc.Netsim.Latency = c.Netsim.Latency.String()
	doc.Netsim.Jitter = c.Netsim.Jitter.String()
	doc.Netsim.Loss = c.Netsim.Loss

	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
