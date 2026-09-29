// Package cmd implements the bloomsom command-line interface.
package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/config"
)

// cfgFile is the config path shared by every subcommand (--config).
var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "bloomsom",
	Short: "Local-first multiplayer game server engine",
	Long: `Bloomsom is a local-first multiplayer game server engine.

Typical flow:
  bloomsom init    set up bloomsom.yaml and the SQLite database
  bloomsom start   run the server in the foreground until Ctrl+C`,
	SilenceUsage: true,
}

// Execute runs the root command. It is called by main.main().
func Execute() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", config.DefaultFile, "path to the config file")
}
