// in internal/repository/manager.go
package repository

import (
	"context"
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultRepoURL = "https://github.com/AlwaysSayNo/quorum-based-manifests-governance-test.git"
)

type GitRepositoryFactory interface {
	New(remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets PgpSecrets) (GitRepository, error)
	IdentifyProvider(repoURL string) bool
}

type GitRepository interface {
	// Sync ensures the local clone of the repository is up-to-date with the remote.
	// This should be called periodically and before any read/write operations.
	Sync(ctx context.Context) error

	// HasRevision return true, if revision commit is the part of git repository.
	HasRevision(ctx context.Context, commit string) (bool, error)

	// GetLatestRevision return the last observed revision for the repository.
	GetLatestRevision(ctx context.Context) (string, error)

	// GetChangedFiles returns a list of files that changed between two commits.
	// TODO: change type from FileChange. Because it bounds it straight to the governance module
	GetChangedFiles(ctx context.Context, fromCommit, toCommit string) ([]governancev1alpha1.FileChange, error)

	// PushMSR commits and pushes the generated MSR manifest to the correct folder in the repo.
	PushMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (string, error)

	// PushSignature commits and pushes a governor's signature to the repository.
	// This would be used by your CLI/API server.
	PushSignature(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, governorAlias string, signatureData []byte) (string, error)
}

type PgpSecrets struct {
	PgpKey        string
	PgpPassphrase string
}

// Manager handles the lifecycle of different repository provider instances.
type Manager struct {
	client client.Client
	// Base directory to store local clones
	basePath string
	// List of all available providers factories
	providers []GitRepositoryFactory
	// Cache of initialized providersToMRT, keyed by repo URL
	providersToMRT map[string]GitRepository
	pgpSecrets     PgpSecrets
	mu             sync.Mutex
}

func NewManager(client client.Client, basePath string) *Manager {
	return &Manager{
		client:         client,
		basePath:       basePath,
		providers:      []GitRepositoryFactory{},
		providersToMRT: make(map[string]GitRepository),
		pgpSecrets:     PgpSecrets{},
	}
}

func (m *Manager) GetProviderForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (GitRepository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// TODO: Get the application and its repoURL from the MRT
	// repoURL := mrt.Spec.GitRepository.URL
	repoURL := DefaultRepoURL
	if repoURL == "" {
		return nil, fmt.Errorf("repository URL is not defined in MRT spec")
	}

	// Check if we already have a provider for this URL in our cache
	if provider, ok := m.providersToMRT[repoURL]; ok {
		return provider, nil
	}

	// Generate unique local path for the clone from the repo URL
	repoHash := fmt.Sprintf("%x", sha1.Sum([]byte(repoURL)))
	localPath := filepath.Join(m.basePath, repoHash)

	var newProvider GitRepository
	var err error

	// TODO: Fetch credentials for this repo from a Kubernetes Secret. For now, we'll assume public access (nil auth).
	provider, err := m.findProvider(repoURL, localPath, nil, m.pgpSecrets)
	if err != nil {
		return nil, err
	} else if provider == nil {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}

	// Cache the new provider
	m.providersToMRT[repoURL] = newProvider
	return newProvider, nil
}

func (m *Manager) findProvider(repoURL, localPath string, auth transport.AuthMethod, pgpSecrets PgpSecrets) (GitRepository, error) {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	if idx == -1 {
		return nil, nil
	}
	return m.providers[idx].New(repoURL, localPath, auth, pgpSecrets)
}
