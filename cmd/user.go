package cmd

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnannpm/Bloomsom/internal/auth"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage player and admin accounts",
}

var (
	userCreatePassword    string
	userCreateEmail       string
	userCreateDisplayName string
	userCreateAdmin       bool
)

var userCreateCmd = &cobra.Command{
	Use:   "create <username>",
	Short: "Create a new player or admin account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		if userCreatePassword == "" {
			return fmt.Errorf("password is required (use --password)")
		}

		cfg, db, err := openConfiguredDB(cmd, false)
		if err != nil {
			return err
		}
		defer db.Close()

		svc := auth.NewService(db, cfg.Auth, nil)

		role := auth.RolePlayer
		if userCreateAdmin {
			role = auth.RoleAdmin
		}

		player, err := svc.Register(cmd.Context(), username, userCreatePassword, userCreateEmail, userCreateDisplayName, role)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "created player %s (id: %d, role: %s)\n", player.Username, player.ID, player.Role)
		return nil
	},
}

var userListLimit int

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered player accounts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, db, err := openConfiguredDB(cmd, false)
		if err != nil {
			return err
		}
		defer db.Close()

		svc := auth.NewService(db, cfg.Auth, nil)
		players, err := svc.ListPlayers(cmd.Context(), userListLimit, 0)
		if err != nil {
			return fmt.Errorf("list users: %w", err)
		}

		out := cmd.OutOrStdout()
		if len(players) == 0 {
			fmt.Fprintln(out, "no registered players")
			return nil
		}

		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "ID\tUSERNAME\tROLE\tSTATUS\tCREATED AT\tLAST LOGIN\n")
		for _, p := range players {
			lastLogin := "-"
			if p.LastLoginAt != nil {
				lastLogin = p.LastLoginAt.UTC().Format("2006-01-02 15:04:05Z")
			}
			statusStr := string(p.Status)
			if p.Status == auth.StatusBanned && p.BannedUntil != nil {
				statusStr = fmt.Sprintf("banned until %s", p.BannedUntil.UTC().Format("2006-01-02 15:04:05Z"))
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
				p.ID, p.Username, p.Role, statusStr,
				p.CreatedAt.UTC().Format("2006-01-02 15:04:05Z"), lastLogin)
		}
		return w.Flush()
	},
}

var (
	userBanDuration time.Duration
	userBanReason   string
)

var userBanCmd = &cobra.Command{
	Use:   "ban <username>",
	Short: "Ban a player account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		cfg, db, err := openConfiguredDB(cmd, false)
		if err != nil {
			return err
		}
		defer db.Close()

		svc := auth.NewService(db, cfg.Auth, nil)
		if err := svc.BanPlayer(cmd.Context(), username, userBanDuration, userBanReason); err != nil {
			return fmt.Errorf("ban user: %w", err)
		}

		durStr := "permanently"
		if userBanDuration > 0 {
			durStr = fmt.Sprintf("for %s", userBanDuration)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "banned player %s %s\n", username, durStr)
		return nil
	},
}

var userUnbanCmd = &cobra.Command{
	Use:   "unban <username>",
	Short: "Unban a player account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		cfg, db, err := openConfiguredDB(cmd, false)
		if err != nil {
			return err
		}
		defer db.Close()

		svc := auth.NewService(db, cfg.Auth, nil)
		if err := svc.UnbanPlayer(cmd.Context(), username); err != nil {
			return fmt.Errorf("unban user: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "unbanned player %s\n", username)
		return nil
	},
}

var userResetPassPassword string

var userResetPassCmd = &cobra.Command{
	Use:   "reset-pass <username>",
	Short: "Reset a player's password",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		if userResetPassPassword == "" {
			return fmt.Errorf("new password is required (use --password)")
		}

		cfg, db, err := openConfiguredDB(cmd, false)
		if err != nil {
			return err
		}
		defer db.Close()

		svc := auth.NewService(db, cfg.Auth, nil)
		if err := svc.ResetPassword(cmd.Context(), username, userResetPassPassword); err != nil {
			return fmt.Errorf("reset password: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "password reset for player %s (all active sessions revoked)\n", username)
		return nil
	},
}

func init() {
	userCreateCmd.Flags().StringVar(&userCreatePassword, "password", "", "account password (min 6 characters)")
	_ = userCreateCmd.MarkFlagRequired("password")
	userCreateCmd.Flags().StringVar(&userCreateEmail, "email", "", "optional account email")
	userCreateCmd.Flags().StringVar(&userCreateDisplayName, "display-name", "", "optional display name")
	userCreateCmd.Flags().BoolVar(&userCreateAdmin, "admin", false, "assign admin role to the account")

	userListCmd.Flags().IntVar(&userListLimit, "limit", 50, "maximum number of accounts to list")

	userBanCmd.Flags().DurationVar(&userBanDuration, "duration", 0, "ban duration (e.g. 24h, 7d; omit or 0 for permanent)")
	userBanCmd.Flags().StringVar(&userBanReason, "reason", "", "reason for the ban")

	userResetPassCmd.Flags().StringVar(&userResetPassPassword, "password", "", "new password (min 6 characters)")
	_ = userResetPassCmd.MarkFlagRequired("password")

	userCmd.AddCommand(userCreateCmd, userListCmd, userBanCmd, userUnbanCmd, userResetPassCmd)
	rootCmd.AddCommand(userCmd)
}
