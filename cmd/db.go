package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/storage"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage the SQLite database",
}

var dbMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply pending schema migrations for the configured preset",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, db, err := openConfiguredDB(cmd, true)
		if err != nil {
			return err
		}
		defer db.Close()

		applied, err := db.Migrate(cmd.Context(), cfg.Game.Preset)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		out := cmd.OutOrStdout()
		if len(applied) == 0 {
			fmt.Fprintln(out, "schema is up to date")
			return nil
		}
		for _, id := range applied {
			fmt.Fprintf(out, "applied %s\n", id)
		}
		return nil
	},
}

var dbStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show applied migrations and tables",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, db, err := openConfiguredDB(cmd, false)
		if err != nil {
			return err
		}
		defer db.Close()
		fmt.Fprintf(cmd.OutOrStdout(), "database %s\n\n", db.Path())
		return printDBStatus(cmd, cmd.OutOrStdout(), db)
	},
}

var createTableCols []string

var dbCreateTableCmd = &cobra.Command{
	Use:   "create-table <name>",
	Short: "Create a custom table (prefixed with custom_)",
	Long: `Create a custom table named custom_<name>. Every table gets an
"id INTEGER PRIMARY KEY" and a "created_at" column automatically.

Names must match ^[a-z_][a-z0-9_]{0,62}$. Allowed types: INTEGER, REAL, TEXT, BLOB.

Example:
  bloomsom db create-table inventory --col item:text:notnull --col qty:integer`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cols, err := storage.ParseColumns(createTableCols)
		if err != nil {
			return err
		}
		_, db, err := openConfiguredDB(cmd, true)
		if err != nil {
			return err
		}
		defer db.Close()

		table, err := db.CreateCustomTable(cmd.Context(), args[0], cols)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "created table %s\n", table)
		return nil
	},
}

func init() {
	dbCreateTableCmd.Flags().StringArrayVar(&createTableCols, "col", nil, "column spec name:TYPE[:notnull] (repeatable)")
	_ = dbCreateTableCmd.MarkFlagRequired("col")

	dbCmd.AddCommand(dbMigrateCmd, dbStatusCmd, dbCreateTableCmd)
	rootCmd.AddCommand(dbCmd)
}

// openConfiguredDB loads the config and opens its database. When create is
// false a missing database file is reported instead of created.
func openConfiguredDB(cmd *cobra.Command, create bool) (config.Config, *storage.DB, error) {
	cfg, _, err := config.Load(cfgFile)
	if err != nil {
		return cfg, nil, fmt.Errorf("load config %s: %w", cfgFile, err)
	}
	if !create {
		if _, err := os.Stat(cfg.Database.Path); errors.Is(err, fs.ErrNotExist) {
			return cfg, nil, fmt.Errorf("database %s does not exist, run `bloomsom init` first", cfg.Database.Path)
		}
	}
	db, err := storage.Open(cmd.Context(), cfg.Database.Path)
	if err != nil {
		return cfg, nil, fmt.Errorf("open database: %w", err)
	}
	return cfg, db, nil
}
