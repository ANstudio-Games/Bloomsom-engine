package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/storage"
	"github.com/adnannpm/Bloomsom/internal/version"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active config, database file and schema state",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()
		cfg, found, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config %s: %w", cfgFile, err)
		}

		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		source := cfgFile
		if !found {
			source = cfgFile + " (not found, SANDBOX defaults)"
		}
		fmt.Fprintf(w, "engine\t%s (protocol %d)\n", version.Engine, version.Protocol)
		fmt.Fprintf(w, "config\t%s\n", source)
		fmt.Fprintf(w, "game\t%s\n", cfg.Game.Name)
		fmt.Fprintf(w, "preset\t%s\n", cfg.Game.Preset)
		fmt.Fprintf(w, "loop\t%s (tick_rate %d)\n", cfg.Engine.Loop, cfg.Engine.TickRate)
		fmt.Fprintf(w, "host\t%s\n", cfg.Server.Host)
		fmt.Fprintf(w, "transports\t%s (ws %d, udp %d)\n", strings.Join(cfg.Server.Transports, ","), cfg.Server.WSPort, cfg.Server.UDPPort)
		fmt.Fprintf(w, "log\t%s/%s\n", cfg.Log.Level, cfg.Log.Format)
		fmt.Fprintf(w, "database\t%s\n", cfg.Database.Path)
		if err := w.Flush(); err != nil {
			return err
		}

		// Do not create the database just by asking for status.
		if _, err := os.Stat(cfg.Database.Path); errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintln(out, "\ndatabase file does not exist yet, run `bloomsom init` or `bloomsom start`")
			return nil
		}
		db, err := storage.Open(cmd.Context(), cfg.Database.Path)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()
		fmt.Fprintln(out)
		return printDBStatus(cmd, out, db)
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

// printDBStatus prints applied migrations and tables with row counts.
func printDBStatus(cmd *cobra.Command, out io.Writer, db *storage.DB) error {
	migrations, err := db.AppliedMigrations(cmd.Context())
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	tables, err := db.Tables(cmd.Context())
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "MIGRATION\tAPPLIED AT\n")
	for _, m := range migrations {
		fmt.Fprintf(w, "%s\t%s\n", m.ID, m.AppliedAt.UTC().Format("2006-01-02 15:04:05Z"))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "TABLE\tROWS\tKIND\n")
	for _, t := range tables {
		kind := "custom"
		if t.Builtin {
			kind = "builtin"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", t.Name, t.Rows, kind)
	}
	return w.Flush()
}
