package cmd

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	display "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/display"
	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
)

const (
	DefaultMSROutputMethod          = "pretty"
	QubmangoIndexFileRepositoryPath = "/.qubmango/index.yaml"
)

func init() {
	getCmd := &cobra.Command{Use: "get", Short: "Get a resource"}

	// Default output format
	outputFormat := DefaultMSROutputMethod

	getMSRCmd := &cobra.Command{
		Use:   "msr",
		Short: "Fetch and show the current active Manifest Signing Request",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get the repo info for repoAlias
			alias, err := getRepoAlias()
			if err != nil {
				return fmt.Errorf("get repository alias: %w", err)
			}

			repositoryInfo, err := cliConfig.GetRepository(alias)
			if err != nil {
				return fmt.Errorf("get repository by alias %s: %w", alias, err)
			}

			sshPass, err := getSSHPassphrase(cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("get SSH passphrase: %w", err)
			}

			// Initialize the repository provider
			conf := &manager.GovernorRepositoryConfig{
				GitRepositoryURL: repositoryInfo.URL,
				SSHSecretPath:    repositoryInfo.SSHKeyPath,
				SSHPassphrase:    sshPass,
				PGPSecretPath:    repositoryInfo.PGPKeyPath,
			}
			repoProvider, err := repoManager.GetProvider(ctx, conf)
			if err != nil {
				return fmt.Errorf("get git provider: %w", err)
			}

			// Take QubmangoIndex from the repository
			qubmangoIndex, err := repoProvider.GetQubmangoIndex(ctx)
			if err != nil {
				return fmt.Errorf("get QubmangoIndex: %w", err)
			}

			// Get policy from qubmango index
			policy, err := getGovernancePolicy(qubmangoIndex, mrtAlias)
			if err != nil {
				return fmt.Errorf("get governance policy: %w", err)
			}

			// Fetch the latest MSR, its and governors signatures
			activeMSR, msrBytes, appSign, governSigns, err := repoProvider.GetActiveMSR(ctx, policy)
			if err != nil {
				return fmt.Errorf("get MSR information from repository: %w", err)
			}

			// Get changed files from repository between previous approved and current commits 
			changedFilesGit, err := repoProvider.GetChangedFilesRaw(ctx, activeMSR.Spec.PreviousCommitSHA, activeMSR.Spec.CommitSHA, activeMSR.Spec.Location.SourcePath)
			if err != nil {
				return fmt.Errorf("get changed files from repository: %w", err)
			}

			// Display the output based on the format flag
			if outputFormat == "raw" {
				return display.PrintMSRRaw(cmd.OutOrStdout(), activeMSR)
			}
			return display.PrintMSRTable(cmd.OutOrStdout(), activeMSR, msrBytes, appSign, governSigns, changedFilesGit)
		},
	}

	getMSRCmd.Flags().StringVarP(&outputFormat, "output", "o", "pretty", "Output format. One of: pretty, raw")
	getMSRCmd.Flags().StringVarP(&repoAlias, "repo", "r", "", "Alias of the repository to use (instead of the value from config file)")
	getMSRCmd.Flags().StringVarP(&mrtAlias, "mrt", "", "", "Alias of the ManifestRequestTemplate resource (required, if index file contains more than 1 entry. Format: <namespace:name>, e.g. `--mrt mrt-ns:mrt-name`)")

	getCmd.AddCommand(getMSRCmd)
	rootCmd.AddCommand(getCmd)
}

// getGovernancePolicy returns specific policy by MRT alias. If QubmangoIndex contains only one policy - alias can be empty string.
// If QubmangoIndex contains >= 1 policies - alias required.
func getGovernancePolicy(
	qubmangoIndex *manager.QubmangoIndex,
	alias string,
) (*manager.QubmangoPolicy, error) {
	if len(qubmangoIndex.Spec.Policies) == 1 && alias == "" {
		return &qubmangoIndex.Spec.Policies[0], nil
	}

	if len(qubmangoIndex.Spec.Policies) > 1 && alias == "" {
		return nil, fmt.Errorf("index file contains more than 1 entry and no mrtAlias was provided")
	}

	// Find policy with mrtAlias and fetch governanceFolderPath
	governanceIndex := slices.IndexFunc(qubmangoIndex.Spec.Policies, func(p manager.QubmangoPolicy) bool {
		return p.Alias == mrtAlias
	})

	if governanceIndex == -1 || len(qubmangoIndex.Spec.Policies) == 0 {
		return nil, fmt.Errorf("no ManifestRequestTemplate index found for mrtAlias %s", mrtAlias)
	}

	return &qubmangoIndex.Spec.Policies[governanceIndex], nil
}
