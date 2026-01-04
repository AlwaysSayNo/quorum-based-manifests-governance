package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

	display "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/display"
)

func init() {
	signCmd := &cobra.Command{
		Use:   "sign",
		Short: "Sign a resource",
	}

	signMsrCmd := &cobra.Command{
		Use:   "msr <version>",
		Short: "Sign the specified MSR file and push the signature",
		Long: `Signs the content of an MSR file. This command requires MSR version to be passed, ensuring you sign exactly what you have reviewed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("parse version flag: %w", err)
			}
			if version < 1 {
				return fmt.Errorf("version should be positive, non-null")
			}

			repoProvider, err := getRepositoryProviderWithInput(true, true, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("get repository provider: %w", err)
			}

			latestMSR, msrBytes, appSignature, governorSignatures, err := fetchLatestMSR(repoProvider)
			if err != nil {
				return fmt.Errorf("get MSR information from repository: %w", err)
			}

			if latestMSR.Spec.Version != version {
				return fmt.Errorf("specified version %d is not the latest", version)
			}
			if latestMSR.Spec.Status != dto.InProgress {
				return fmt.Errorf("only in-progress MSR can be signed (current status: %s)", latestMSR.Spec.Status)
			}

			// Get changed files from repository between previous approved and current commits
			changedFilesGit, err := repoProvider.GetChangedFilesRaw(ctx, latestMSR.Spec.PreviousCommitSHA, latestMSR.Spec.CommitSHA, latestMSR.Spec.Locations.SourcePath)
			if err != nil {
				return fmt.Errorf("get changed files from repository: %w", err)
			}

			err = display.PrintIfVerifyFails(cmd.OutOrStdout(), latestMSR, msrBytes, appSignature, governorSignatures, changedFilesGit)
			if err != nil {
				return err
			}

			user, err := getUserInfoValidated()
			if err != nil {
				return err
			}

			commit, err := repoProvider.PushGovernorSignature(ctx, latestMSR, user)
			if err != nil {
				return fmt.Errorf("sign MSR version %d: %w", version, err)
			}

			cmd.Printf("Successfully signed and pushed signature for MSR %s:%s, version %d (commit: %s)\n", latestMSR.ObjectMeta.Namespace, latestMSR.ObjectMeta.Name, version, commit)
			return nil
		},
	}

	signMsrCmd.Flags().StringVarP(&repoAlias, "repo", "r", "", "Alias of the repository to use (instead of the value from config file)")
	signMsrCmd.Flags().StringVarP(&mrtAlias, "mrt", "", "", "Alias of the ManifestRequestTemplate resource (required, if index file contains more than 1 entry. Format: <namespace:name>, e.g. `--mrt mrt-ns:mrt-name`)")

	signCmd.AddCommand(signMsrCmd)
	rootCmd.AddCommand(signCmd)
}
