package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the engine and protocol versions",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "bloomsom %s (protocol %d, %s %s/%s)\n",
			version.Engine, version.Protocol, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
