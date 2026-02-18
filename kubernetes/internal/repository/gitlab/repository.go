package gitlab

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

type GitLabProviderFactory struct {
}

// New creates and initializes a gitLabProvider
func (f *GitLabProviderFactory) New(
	ctx context.Context,
	remoteURL, localPath string,
	auth transport.AuthMethod,
	pgpSecrets repository.PgpSecrets,
) (repository.GitRepository, error) {
	base := repository.NewBaseGitProvider(
		remoteURL,
		localPath,
		auth,
		log.FromContext(ctx),
		pgpSecrets,
	)

	p := &gitLabProvider{
		BaseGitProvider: base,
	}

	// Sync on creation
	if err := p.Sync(context.Background()); err != nil {
		return nil, fmt.Errorf("initial sync failed: %w", err)
	}
	return p, nil
}

func (f *GitLabProviderFactory) IdentifyProvider(
	repoURL string,
) bool {
	return strings.Contains(repoURL, "gitlab.com")
}

// gitLabProvider is the GitLab-specific implementation of GitRepository.
type gitLabProvider struct {
	*repository.BaseGitProvider
}

// GitLab-specific methods here
