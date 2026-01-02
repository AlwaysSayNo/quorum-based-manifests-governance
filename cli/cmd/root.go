package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository/github"
)

var (
	repoAlias string
	mrtAlias string
	cliConfig   config.Config
	repoManager *manager.Manager
	ctx context.Context
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
