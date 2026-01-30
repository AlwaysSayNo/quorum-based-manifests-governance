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

func fetchAllMCA(
	repoProvider manager.GitRepositoryProvider,
) ([]manager.MCAInfo, error) {
	// Get the repo info for repoAlias
	repoInfo, err := getCurrentRepo()
	if err != nil {
		return nil, fmt.Errorf("get current repo: %w", err)
	}

	// Convert current repo information to governance policy
	policy := &manager.GovernancePolicy{
		GovernancePath: repoInfo.GovernanceFolderPath,
		MSRName:        repoInfo.MSRName,
		MCAName:        repoInfo.MCAName,
	}

	// Fetch the latest MSR and its signature
	return repoProvider.GetMCAHistory(ctx, policy)
}

func fetchLatestMSR(
	repoProvider manager.GitRepositoryProvider,
) (*manager.MSRInfo, error) {
	// Get the repo info for repoAlias
	repoInfo, err := getCurrentRepo()
	if err != nil {
		return nil, fmt.Errorf("get current repo: %w", err)
	}

	// Convert current repo information to governance policy
	policy := &manager.GovernancePolicy{
		GovernancePath: repoInfo.GovernanceFolderPath,
		MSRName:        repoInfo.MSRName,
		MCAName:        repoInfo.MCAName,
	}

	// Fetch the latest MSR, its and governors signatures
	return repoProvider.GetLatestMSR(ctx, policy)
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

func getRepositoryProviderWithInput(
	requestSSH, requestPGP bool,
	w io.Writer,
) (manager.GitRepositoryProvider, error) {
	sshPass := ""
	var err error

	if requestSSH {
		sshPass, err = getSSHPassphrase(w)
		if err != nil {
			return nil, fmt.Errorf("get SSH passphrase: %w", err)
		}
	}

	pgpPass := ""
	if requestPGP {
		pgpPass, err = getPGPPassphrase(w)
		if err != nil {
			return nil, fmt.Errorf("get PGP passphrase: %w", err)
		}
	}

	return getRepositoryProvider(sshPass, pgpPass)
}

func getRepositoryProvider(
	sshPass, pgpPass string,
) (manager.GitRepositoryProvider, error) {
	// Get the repo info for repoAlias
	repositoryInfo, err := getCurrentRepo()
	if err != nil {
		return nil, fmt.Errorf("get current repo: %w", err)
	}

	// Initialize the repository provider
	conf := &manager.GovernorRepositoryConfig{
		GitRepositoryURL: repositoryInfo.URL,
		SSHSecretPath:    repositoryInfo.SSHKeyPath,
		SSHPassphrase:    sshPass,
		PGPSecretPath:    repositoryInfo.PGPKeyPath,
		PGPPassphrase:    pgpPass,
	}
	return repoManager.GetProvider(ctx, conf)
}

func getCurrentRepo() (*config.GitRepository, error) {
	alias, err := getRepoAlias()
	if err != nil {
		return nil, fmt.Errorf("get repository alias: %w", err)
	}

	repositoryInfo, err := cliConfig.GetRepository(alias)
	if err != nil {
		return nil, fmt.Errorf("get repository by alias %s: %w", alias, err)
	}

	return &repositoryInfo, nil
}

func getSSHPassphrase(
	w io.Writer,
) (string, error) {
	if sshPassphrase != "" {
		return sshPassphrase, nil
	}
	sshPassphrase, err := getSecretInput(w, "Enter SSH passphrase: ")
	return sshPassphrase, err
}

func getPGPPassphrase(
	w io.Writer,
) (string, error) {
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

func getUserInfoValidated() (config.UserInfo, error) {
	user := cliConfig.GetData().User
	if user.Email == "" || user.Name == "" {
		return config.UserInfo{}, fmt.Errorf("user information missing (email: %s, name: %s)", user.Email, user.Name)
	}

	return user, nil
}
