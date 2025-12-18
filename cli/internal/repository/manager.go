package repository

import (
	"context"
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GitRepositoryFactory interface {
	New(ctx context.Context, remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets Secrets) (GitRepository, error)
	IdentifyProvider(repoURL string) bool
}

type GitRepository interface {
	Sync(ctx context.Context) error
	HasRevision(ctx context.Context, commit string) (bool, error)
	GetLatestRevision(ctx context.Context) (string, error)
	GetChangedFiles(ctx context.Context, fromCommit, toCommit string, fromFolder string) ([]FileChange, error)
	PushSignature(ctx context.Context, msr *ManifestSigningRequestManifestObject, governorAlias string, signatureData []byte) (string, error)
}

type Secrets struct {
	PrivateKey string
	Passphrase string
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
	mu             sync.Mutex
}

func NewManager(client client.Client, basePath string) *Manager {
	return &Manager{
		client:         client,
		basePath:       basePath,
		providers:      []GitRepositoryFactory{},
		providersToMRT: make(map[string]GitRepository),
	}
}

func (m *Manager) Register(factory GitRepositoryFactory) error {
	m.providers = append(m.providers, factory)
	return nil
}

type GovernorRepositoryConfig struct {
	GitRepositoryURL string
	PGPSecrets       *Secrets
	SSHSecrets       *Secrets
}

func (m *Manager) GetProviderForMRT(ctx context.Context, conf *GovernorRepositoryConfig) (GitRepository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoURL := conf.GitRepositoryURL
	if repoURL == "" {
		return nil, fmt.Errorf("repository URL is not defined in MRT spec")
	}
	if !m.providerExists(repoURL) {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}

	// Check if we already have a provider for this URL in our cache
	if provider, ok := m.providersToMRT[repoURL]; ok {
		return provider, nil
	}

	// Generate unique local path for the clone from the repo URL
	repoHash := fmt.Sprintf("%x", sha1.Sum([]byte(repoURL)))
	localPath := filepath.Join(m.basePath, repoHash)

	// Sync pgp and ssh secrets

	if conf.PGPSecrets == nil {
		return nil, fmt.Errorf("pgp information is nil")
	}
	sshSecrets, err := m.syncSSHSecrets(ctx, conf)
	if err != nil {
		return nil, err
	}

	provider, err := m.findProvider(ctx, repoURL, localPath, sshSecrets, *conf.PGPSecrets)
	if err != nil {
		return nil, err
	}

	// Cache the new provider
	m.providersToMRT[repoURL] = provider
	return provider, nil
}

func (m *Manager) providerExists(repoURL string) bool {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	return idx != -1
}

func (m *Manager) findProvider(ctx context.Context, repoURL, localPath string, auth transport.AuthMethod, pgpSecrets Secrets) (GitRepository, error) {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	if idx == -1 {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}
	return m.providers[idx].New(ctx, repoURL, localPath, auth, pgpSecrets)
}

func (m *Manager) syncSSHSecrets(ctx context.Context, conf *GovernorRepositoryConfig) (*ssh.PublicKeys, error) {
	if conf.SSHSecrets == nil {
		return nil, fmt.Errorf("ssh information is nil")
	}

	privateKeyBytes := []byte(conf.SSHSecrets.PrivateKey)
	passphraseBytes := []byte(conf.SSHSecrets.Passphrase)

	// Decrypt the private key.
	publicKeys, err := ssh.NewPublicKeys("git", privateKeyBytes, string(passphraseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create public keys from secret: %w", err)
	}

	return publicKeys, nil
}
