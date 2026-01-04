package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	display "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/display"
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
			repoProvider, err := getRepositoryProviderWithInput(true, false, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("get repository provider: %w", err)
			}

			latestMSR, msrBytes, appSign, governSigns, err := fetchLatestMSR(repoProvider)
			if err != nil {
				return fmt.Errorf("get MSR information from repository: %w", err)
			}

			// Get changed files from repository between previous approved and current commits 
			changedFilesGit, err := repoProvider.GetChangedFilesRaw(ctx, latestMSR.Spec.PreviousCommitSHA, latestMSR.Spec.CommitSHA, latestMSR.Spec.Locations.SourcePath)
			if err != nil {
				return fmt.Errorf("get changed files from repository: %w", err)
			}

			// Display the output based on the format flag
			if outputFormat == "raw" {
				return display.PrintMSRRaw(cmd.OutOrStdout(), latestMSR, msrBytes, appSign, governSigns, changedFilesGit)
			}
			return display.PrintMSRTable(cmd.OutOrStdout(), latestMSR, msrBytes, appSign, governSigns, changedFilesGit)
		},
	}

	// Create the `diff msr` subcommand
	diffMsrCmd := &cobra.Command{
		Use:   "file-diff",
		Short: "Show the git diff for all changed files in the active MSR",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoProvider, err := getRepositoryProviderWithInput(true, false, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("get repository provider: %w", err)
			}

			latestMSR, msrBytes, appSign, governSigns, err := fetchLatestMSR(repoProvider)
			if err != nil {
				return fmt.Errorf("get MSR information from repository: %w", err)
			}

			// Get changed files from repository between previous approved and current commits 
			changedFilesGit, err := repoProvider.GetChangedFilesRaw(ctx, latestMSR.Spec.PreviousCommitSHA, latestMSR.Spec.CommitSHA, latestMSR.Spec.Locations.SourcePath)
			if err != nil {
				return fmt.Errorf("get changed files from repository: %w", err)
			}

			// Get patches for files from MSR
			patches, err := repoProvider.GetFileDiffPatchParts(ctx, latestMSR, latestMSR.Spec.PreviousCommitSHA, latestMSR.Spec.CommitSHA)
			if err != nil {
				return fmt.Errorf("get diff for MSR version %d", latestMSR.Spec.Version)
			}
			
			// Display the file diffs
			return display.PrintMSRFileDiffs(cmd.OutOrStdout(), latestMSR, msrBytes, appSign, governSigns, changedFilesGit, patches)
		},
	}

	getMSRCmd.Flags().StringVarP(&outputFormat, "output", "o", "pretty", "Output format. One of: pretty, raw")
	getMSRCmd.Flags().StringVarP(&repoAlias, "repo", "r", "", "Alias of the repository to use (instead of the value from config file)")
	getMSRCmd.Flags().StringVarP(&mrtAlias, "mrt", "", "", "Alias of the ManifestRequestTemplate resource (required, if index file contains more than 1 entry. Format: <namespace:name>, e.g. `--mrt mrt-ns:mrt-name`)")

	getCmd.AddCommand(getMSRCmd)
	getCmd.AddCommand(diffMsrCmd)
	rootCmd.AddCommand(getCmd)
}
