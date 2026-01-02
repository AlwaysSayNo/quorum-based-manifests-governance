package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	configCmd := &cobra.Command{Use: "config", Short: "Manage CLI configuration"}

	showConfigCmd := &cobra.Command{
		Use:   "show",
		Short: "Print content of the config file",
		Args: cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			configData := cliConfig.GetData()
			fmt.Print(configData.StringYaml())

			return nil
		},
	}

	addRepoCmd := &cobra.Command{
		Use:   "add-repo <alias> <url> <ssh-key-path> <pgp-key-path>",
		Short: "Add a new repository configuration",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, url, sshKeyPath, pgpKeyPath := args[0], args[1], args[2], args[3]

			// Load config, add the new repo, and save the config file.
			return cliConfig.AddRepository(alias, url, sshKeyPath, pgpKeyPath)
		},
	}

	editRepoCmd := &cobra.Command{
		Use:   "edit-repo <alias> <url> <ssh-key-path> <pgp-key-path>",
		Short: "Edit existing repository configuration",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, url, sshKeyPath, pgpKeyPath := args[0], args[1], args[2], args[3]

			// Load config, edit existing repo, and save the config file.
			return cliConfig.EditRepository(alias, url, sshKeyPath, pgpKeyPath)
		},
	}

	removeRepoCmd := &cobra.Command{
		Use:   "remove-repo <alias>",
		Short: "Remove existing repository configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]

			// Load config, remove existing repo, and save the config file.
			return cliConfig.RemoveRepository(alias)
		},
	}

	useRepoCmd := &cobra.Command{
		Use:   "use-repo <alias>",
		Short: "Set current used repository variable to avoid specifying it each time",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]

			// Load config, remember repository alias to use by default (or empty), and save the config file.
			return cliConfig.UseRepository(alias)
		},
	}

	setUserCmd := &cobra.Command{
		Use:   "set-user <username> <email>",
		Short: "Set user information used in commits",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			username, email := args[0], args[1]

			// Load config, set user information, and save the config file.
			return cliConfig.SetUser(username, email)
		},
	}

	configCmd.AddCommand(showConfigCmd)
	configCmd.AddCommand(addRepoCmd)
	configCmd.AddCommand(useRepoCmd)
	configCmd.AddCommand(editRepoCmd)
	configCmd.AddCommand(removeRepoCmd)
	configCmd.AddCommand(setUserCmd)

	rootCmd.AddCommand(configCmd)
}
