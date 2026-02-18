package bitbucket

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

type BitbucketProviderFactory struct {
}

// New creates and initializes a bitbucketProvider
func (f *BitbucketProviderFactory) New(
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

	p := &bitbucketProvider{
		BaseGitProvider: base,
	}

	// Sync on creation
	if err := p.Sync(context.Background()); err != nil {
		return nil, fmt.Errorf("initial sync failed: %w", err)
	}
	return p, nil
}

func (f *BitbucketProviderFactory) IdentifyProvider(
	repoURL string,
) bool {
	return strings.Contains(repoURL, "bitbucket.org")
}

// bitbucketProvider is the Bitbucket-specific implementation of GitRepository.
type bitbucketProvider struct {
	*repository.BaseGitProvider
}

// Bitbucket-specific methods here
