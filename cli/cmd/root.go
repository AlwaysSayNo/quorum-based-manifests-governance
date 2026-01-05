package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

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

func fetchLatestMSR(
	repoProvider manager.GitRepositoryProvider,
) (*dto.ManifestSigningRequestManifestObject, []byte, dto.SignatureData, []dto.SignatureData, error) {

	// Take QubmangoIndex from the repository
	qubmangoIndex, err := repoProvider.GetQubmangoIndex(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get QubmangoIndex: %w", err)
	}

	// Get policy from qubmango index
	policy, err := getGovernancePolicy(qubmangoIndex, mrtAlias)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get governance policy: %w", err)
	}

	// Fetch the latest MSR, its and governors signatures
	return repoProvider.GetLatestMSR(ctx, policy)
}

// getGovernancePolicy returns specific policy by MRT alias. If QubmangoIndex contains only one policy - alias can be empty string.
// If QubmangoIndex contains >= 1 policies - alias required.
func getGovernancePolicy(
	qubmangoIndex *dto.QubmangoIndex,
	alias string,
) (*dto.QubmangoPolicy, error) {
	if len(qubmangoIndex.Spec.Policies) == 1 && alias == "" {
		return &qubmangoIndex.Spec.Policies[0], nil
	}

	if len(qubmangoIndex.Spec.Policies) > 1 && alias == "" {
		return nil, fmt.Errorf("index file contains more than 1 entry and no mrtAlias was provided")
	}

	// Find policy with mrtAlias and fetch governanceFolderPath
	governanceIndex := slices.IndexFunc(qubmangoIndex.Spec.Policies, func(p dto.QubmangoPolicy) bool {
		return p.Alias == mrtAlias
	})

	if governanceIndex == -1 || len(qubmangoIndex.Spec.Policies) == 0 {
		return nil, fmt.Errorf("no ManifestRequestTemplate index found for mrtAlias %s", mrtAlias)
	}

	return &qubmangoIndex.Spec.Policies[governanceIndex], nil
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
		// sshPass, err = getSSHPassphrase(w)
		sshPass = "Account 123"
		if err != nil {
			return nil, fmt.Errorf("get SSH passphrase: %w", err)
		}
	}

	pgpPass := ""
	if requestPGP {
		// pgpPass, err = getPGPPassphrase(w)
		pgpPass = "ownerpassphrase"
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
	alias, err := getRepoAlias()
	if err != nil {
		return nil, fmt.Errorf("get repository alias: %w", err)
	}

	repositoryInfo, err := cliConfig.GetRepository(alias)
	if err != nil {
		return nil, fmt.Errorf("get repository by alias %s: %w", alias, err)
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
