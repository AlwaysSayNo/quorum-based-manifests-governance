package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crypto "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/crypto"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
)

type GitHubProviderFactory struct {
}

// New creates and initializes a gitProvider
func (f *GitHubProviderFactory) New(
	ctx context.Context,
	remoteURL, localPath string,
	auth transport.AuthMethod,
	pgpSecrets crypto.Secrets,
) (repository.GitRepositoryProvider, error) {
	base := repository.NewBaseGitProvider(
		remoteURL,
		localPath,
		auth,
		log.FromContext(ctx).WithName("github-repository"),
		pgpSecrets,
	)

	p := &gitHubProvider{
		BaseGitProvider: base,
	}

	// Sync on creation
	if err := p.Sync(context.Background()); err != nil {
		return nil, fmt.Errorf("initial sync failed: %w", err)
	}
	return p, nil
}

func (f *GitHubProviderFactory) IdentifyProvider(
	repoURL string,
) bool {
	return strings.Contains(repoURL, "github.com")
}

type gitHubProvider struct {
	*repository.BaseGitProvider
}
