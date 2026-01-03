package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository/github"
)

var (
	repoAlias     string
	mrtAlias      string
	sshPassphrase string
	pgpPassphrase string
	cliConfig     config.Config
	repoManager   *manager.Manager
	ctx           context.Context
)

var rootCmd = &cobra.Command{
	Use:   "qubmango",
	Short: "A CLI for Quorum Based Manifests Governance",
	Long:  `qubmango allows governors to review, inspect, and sign manifest signing requests for their applications.`,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		ctx = context.Background()

		// Load the config file
		var err error
		cliConfig, err = config.LoadConfig()
		if err != nil {
			return err
		}

		// Register repository manager
		repoManager = repositoryManager()

		return nil
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
}

func repositoryManager() *manager.Manager {
	m := manager.NewManager()
	m.Register(&github.GitProviderFactory{})

	return m
}

func getRepoAlias() (string, error) {
	currRepo := cliConfig.GetData().CurrentRepository

	if repoAlias != "" {
		return repoAlias, nil
	} else if currRepo != "" {
		return currRepo, nil
	}

	return "", fmt.Errorf("no current repository is set")
}

func getSSHPassphrase(w io.Writer) (string, error) {
	if sshPassphrase != "" {
		return sshPassphrase, nil
	}
	sshPassphrase, err := getSecretInput(w, "Enter SSH passphrase: ")
	return sshPassphrase, err
}

func getPGPPassphrase(w io.Writer) (string, error) {
	if pgpPassphrase != "" {
		return pgpPassphrase, nil
	}
	pgpPassphrase, err := getSecretInput(w, "Enter PGP passphrase: ")
	return pgpPassphrase, err
}

func getSecretInput(
	w io.Writer,
	msg string,
) (string, error) {
	fmt.Fprint(w, msg)

	// Cast os.Stdin.Fd() to int for the syscall
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", fmt.Errorf("failed to read secret input: %w", err)
	}
	fmt.Fprintln(w)

	return string(bytePassword), nil
}
