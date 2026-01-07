package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	display "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/display"
)

func init() {
	historyCmd := &cobra.Command{Use: "history", Short: "Show history of requests, approves"}

	historyMCACmd := &cobra.Command{
		Use:   "mca",
		Short: "Fetch and show the history of Manifest Change Approval",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoProvider, err := getRepositoryProviderWithInput(true, false, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("get repository provider: %w", err)
			}

			// Get latest MSR information
			mcaInfos, err := fetchAllMCA(repoProvider)
			if err != nil {
				return fmt.Errorf("get MSR information from repository: %w", err)
			}

			// Display the output
			return display.PrintMCAHistoryTable(cmd.OutOrStdout(), mcaInfos)
		},
	}

	historyCmd.Flags().StringVarP(&repoAlias, "repo", "r", "", "Alias of the repository to use (instead of the value from config file)")
	historyCmd.Flags().StringVarP(&mrtAlias, "mrt", "", "", "Alias of the ManifestRequestTemplate resource (required, if index file contains more than 1 entry. Format: <namespace:name>, e.g. `--mrt mrt-ns:mrt-name`)")

	historyCmd.AddCommand(historyMCACmd)
	rootCmd.AddCommand(historyCmd)
}
