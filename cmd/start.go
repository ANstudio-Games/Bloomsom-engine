package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/logging"
	"github.com/adnannpm/Bloomsom/internal/server"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Run the game server in the foreground until Ctrl+C",
	Long: `Run the game server in the foreground. Logs stream to stdout (and to
log.file when set) until the process receives SIGINT or SIGTERM, then the
server shuts down gracefully.

Without a config file the server runs in SANDBOX mode using defaults.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, found, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config %s: %w", cfgFile, err)
		}

		logger, closer, err := logging.New(logging.Options{
			Level:  cfg.Log.Level,
			Format: cfg.Log.Format,
			File:   cfg.Log.File,
		}, cmd.OutOrStdout())
		if err != nil {
			return fmt.Errorf("set up logging: %w", err)
		}
		defer closer.Close()

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return server.Run(ctx, server.Options{
			Config:     cfg,
			ConfigPath: cfgFile,
			Sandbox:    !found,
			Logger:     logger,
		})
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
