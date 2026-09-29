package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/storage"
)

var (
	initPreset string
	initName   string
	initYes    bool
	initForce  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up bloomsom.yaml and the SQLite database",
	Long: `Create bloomsom.yaml for the chosen game preset and initialize the SQLite
database with the built-in auth tables plus the preset tables.

Presets:
  realtime-action  tick loop 60 TPS, UDP + WebSocket
  turn-based       event loop, WebSocket
  lobby-chat       event loop, WebSocket
  custom           built-in tables only, tune bloomsom.yaml by hand

Run without flags for an interactive wizard, or non-interactively:
  bloomsom init --preset turn-based --name my-chess --yes`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initPreset, "preset", "", "game preset: "+strings.Join(config.Presets, " | "))
	initCmd.Flags().StringVar(&initName, "name", "", "game name")
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "non-interactive, accept defaults")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite an existing config file")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	preset := initPreset
	if preset == "" {
		preset = config.PresetLobbyChat
	}
	if !slices.Contains(config.Presets, preset) {
		return fmt.Errorf("unknown preset %q (valid: %s)", preset, strings.Join(config.Presets, ", "))
	}
	name := initName
	if name == "" {
		name = "my-game"
	}

	var wsPort int
	if !initYes {
		p := &prompter{in: bufio.NewReader(cmd.InOrStdin()), out: out}
		fmt.Fprintln(out, "Bloomsom setup (press Enter to accept the default in brackets)")
		name = p.text("Game name", name)
		preset = p.choice("Preset", config.Presets, preset)
		wsPort = p.port("WebSocket port", config.Default().Server.WSPort)
		if p.err != nil {
			return p.err
		}
	}

	cfg, err := config.ForPreset(preset, name)
	if err != nil {
		return err
	}
	if wsPort != 0 {
		cfg.Server.WSPort = wsPort
	}

	if err := config.Write(cfgFile, cfg, initForce); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists, use --force to overwrite", cfgFile)
		}
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(out, "wrote %s\n", cfgFile)

	db, err := storage.Open(cmd.Context(), cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	applied, err := db.Migrate(cmd.Context(), cfg.Game.Preset)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	fmt.Fprintf(out, "database %s ready (%d migrations applied)\n", db.Path(), len(applied))
	for _, id := range applied {
		fmt.Fprintf(out, "  + %s\n", id)
	}

	fmt.Fprintf(out, "\ngame %q, preset %s, loop %s, transports %s\n",
		cfg.Game.Name, cfg.Game.Preset, cfg.Engine.Loop, strings.Join(cfg.Server.Transports, ","))
	fmt.Fprintln(out, "next: bloomsom start")
	return nil
}

// prompter reads line-based answers. EOF (for example piped stdin) accepts
// the default. The first read error is kept in err.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
	err error
}

func (p *prompter) text(label, def string) string {
	if p.err != nil {
		return def
	}
	fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		p.err = fmt.Errorf("read input: %w", err)
		return def
	}
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(p.out)
	}
	if v := strings.TrimSpace(line); v != "" {
		return v
	}
	return def
}

func (p *prompter) choice(label string, options []string, def string) string {
	for i, o := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, o)
	}
	for attempt := 0; attempt < 3; attempt++ {
		v := p.text(label+" (name or number)", def)
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(options) {
			return options[n-1]
		}
		if slices.Contains(options, v) {
			return v
		}
		fmt.Fprintf(p.out, "  %q is not a valid choice\n", v)
	}
	p.err = fmt.Errorf("no valid %s chosen", strings.ToLower(label))
	return def
}

func (p *prompter) port(label string, def int) int {
	for attempt := 0; attempt < 3; attempt++ {
		v := p.text(label, strconv.Itoa(def))
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 65535 {
			return n
		}
		fmt.Fprintf(p.out, "  %q is not a port between 1 and 65535\n", v)
	}
	p.err = fmt.Errorf("no valid %s entered", strings.ToLower(label))
	return def
}
