package cmd

import (
	"fmt"
	"os"
	"strings"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
)

func init() {

	configCmd := &cobra.Command{Use: "config", Short: "Manage CLI configuration"}

	showConfigCmd := &cobra.Command{
		Use:   "show",
		Short: "Print content of the config file",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			configData := cliConfig.GetData()
			fmt.Print(configData.StringYaml())

			return nil
		},
	}

	addRepoCmd := &cobra.Command{
		Use:   "add-repo <alias> <url> <ssh-key-path> <pgp-key-path> <governance-key-path>",
		Short: "Add a new repository configuration",
		Args:  cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			goverKey, err := readFile(args[4])
			if err != nil {
				return fmt.Errorf("extract governance public key: %w", err)
			}

			repository := config.GitRepository{
				Alias:               args[0],
				URL:                 args[1],
				SSHKeyPath:          args[2],
				PGPKeyPath:          args[3],
				GovernancePublicKey: goverKey,
			}

			// Load config, add the new repo, and save the config file.
			return cliConfig.AddRepository(repository)
		},
	}

	var editSSHURL string
	var editSSHKeyPath string
	var editPGPKeyPath string
	var editGovernanceKeyPath string

	editRepoCmd := &cobra.Command{
		Use:   "edit-repo <alias>",
		Short: "Edit existing repository configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if editSSHURL == "" && editSSHKeyPath == "" && editPGPKeyPath == "" && editGovernanceKeyPath == "" {
				return fmt.Errorf("no edit field was passed")
			}
			goverKey, err := readFile(editGovernanceKeyPath)
			if err != nil {
				return fmt.Errorf("extract governance public key: %w", err)
			}

			repository := config.GitRepository{
				Alias:               args[0],
				URL:                 editSSHURL,
				SSHKeyPath:          editSSHKeyPath,
				PGPKeyPath:          editPGPKeyPath,
				GovernancePublicKey: goverKey,
			}

			// Load config, edit existing repo, and save the config file.
			return cliConfig.EditRepository(repository)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]

			// Load config, remember repository alias to use by default (or empty), and save the config file.
			return cliConfig.UseRepository(alias)
		},
	}

	setUserCmd := &cobra.Command{
		Use:   "set-user <username> <email>",
		Short: "Set user information used in commits",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			username, email := args[0], args[1]

			// Load config, set user information, and save the config file.
			return cliConfig.SetUser(username, email)
		},
	}

	editRepoCmd.Flags().StringVarP(&editSSHURL, "url", "", "", "SSH URL of the repository")
	editRepoCmd.Flags().StringVarP(&editSSHKeyPath, "ssh-key-path", "", "", "Absolute path to private SSH key, used to connect to the repository")
	editRepoCmd.Flags().StringVarP(&editPGPKeyPath, "pgp-key-path", "", "", "Absolute path to private PGP key, used for signing")
	editRepoCmd.Flags().StringVarP(&editGovernanceKeyPath, "governance-key-path", "", "", "Absolute path to public PGP key of the governance tool")

	configCmd.AddCommand(showConfigCmd)
	configCmd.AddCommand(addRepoCmd)
	configCmd.AddCommand(useRepoCmd)
	configCmd.AddCommand(editRepoCmd)
	configCmd.AddCommand(removeRepoCmd)
	configCmd.AddCommand(setUserCmd)

	rootCmd.AddCommand(configCmd)
}

func readFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("file path cannot be empty")
	}

	// Expand the tilde (~) to the home directory
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not get user home directory: %w", err)
		}
		path = filepath.Join(homeDir, path[2:])
	}

	// Read file 
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read key file '%s': %w", path, err)
	}

	return string(keyBytes), nil
}
