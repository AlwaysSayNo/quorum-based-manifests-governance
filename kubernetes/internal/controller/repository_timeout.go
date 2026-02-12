package controller

import (
	"context"
	"fmt"
	"time"

	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

const defaultGitOperationTimeout = 30 * time.Second

// withGitRepository runs a git repository operation with a bounded context.
// It ensures BOTH provider acquisition and repository calls share the same timed context.
func withGitRepository[T any](
	ctx context.Context,
	timeout time.Duration,
	getRepo func(context.Context) (repomanager.GitRepository, error),
	op func(context.Context, repomanager.GitRepository) (T, error),
) (T, error) {
	var zero T
	if timeout <= 0 {
		timeout = defaultGitOperationTimeout
	}

	gitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	repo, err := getRepo(gitCtx)
	if err != nil {
		return zero, err
	}
	if repo == nil {
		return zero, fmt.Errorf("git repository provider is nil")
	}

	return op(gitCtx, repo)
}
