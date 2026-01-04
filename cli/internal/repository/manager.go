package repository

import (
	"context"
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	crypto "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/crypto"
)

const (
	DefaultReposPath      = "/tmp/qubmango/git/repos"
	QubmangoIndexFilePath = ".qubmango/index.yaml"
)

func NewMyFilePatch(
	filePatch []diff.FilePatch,
	message string,
) *myFilePatch {
	return &myFilePatch{
		filePatch: filePatch,
		message:   message,
	}
}

// myFilePatch is a helper struct that implements the diff.Patch interface. This allows to pass arbiter subset of FilePatches to the encoder.
type myFilePatch struct {
	filePatch []diff.FilePatch
	message   string
}

func (mfp *myFilePatch) FilePatches() []diff.FilePatch {
	if mfp.filePatch == nil {
		return nil
	}
	return mfp.filePatch
}

func (mfp *myFilePatch) Message() string {
	return mfp.message
}

type GitRepositoryFactory interface {
	New(ctx context.Context, remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets crypto.Secrets) (GitRepositoryProvider, error)
	IdentifyProvider(repoURL string) bool
}

type SignatureData []byte

type GitRepositoryProvider interface {
	Sync(ctx context.Context) error
	HasRevision(ctx context.Context, commit string) (bool, error)
	GetLatestRevision(ctx context.Context) (string, error)
	GetChangedFilesRaw(ctx context.Context, fromCommit, toCommit string, fromFolder string) (map[string]FileBytesWithStatus, error)
	PushGovernorSignature(ctx context.Context, msr *ManifestSigningRequestManifestObject, user config.UserInfo) (string, error)
	GetQubmangoIndex(ctx context.Context) (*QubmangoIndex, error)
	GetLatestMSR(ctx context.Context, policy *QubmangoPolicy) (*ManifestSigningRequestManifestObject, []byte, SignatureData, []SignatureData, error)
	GetFileDiffPatchParts(ctx context.Context, msr *ManifestSigningRequestManifestObject, fromCommit, toCommit string) (map[string]diff.Patch, error)
}

// Manager handles the lifecycle of different repository provider instances.
type Manager struct {
	// Base directory to store local clones
	basePath string
	// List of all available providers factories
	providers []GitRepositoryFactory
	// Cache of initialized providersToMRT, keyed by repo URL
	providersToMRT map[string]GitRepositoryProvider
	mu             sync.Mutex
}

func NewManager() *Manager {
	return NewManagerWithPath(DefaultReposPath)
}

func NewManagerWithPath(
	basePath string,
) *Manager {
	return &Manager{
		basePath:       basePath,
		providers:      []GitRepositoryFactory{},
		providersToMRT: make(map[string]GitRepositoryProvider),
	}
}

func (m *Manager) Register(
	factory GitRepositoryFactory,
) error {
	m.providers = append(m.providers, factory)
	return nil
}

type GovernorRepositoryConfig struct {
	GitRepositoryURL string
	SSHSecretPath    string
	SSHPassphrase    string
	PGPSecretPath    string
	PGPPassphrase    string
}

func (m *Manager) GetProvider(
	ctx context.Context,
	conf *GovernorRepositoryConfig,
) (GitRepositoryProvider, error) {
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

	// Sync ssh secrets
	sshSecrets, err := crypto.GetSSHSecrets(conf.SSHSecretPath, conf.SSHPassphrase)
	if err != nil {
		return nil, fmt.Errorf("get ssh secrets: %w", err)
	}
	sshPublicKeys, err := crypto.SyncSSHSecrets(ctx, sshSecrets)
	if err != nil {
		return nil, fmt.Errorf("sync ssh secrets", err)
	}

	// Get pgp secrets
	pgpSecrets, err := crypto.GetPGPSecrets(conf.PGPSecretPath, conf.PGPPassphrase)
	if err != nil {
		return nil, fmt.Errorf("get pgp secrets: %w", err)
	}

	// Find provider
	provider, err := m.findProvider(ctx, repoURL, localPath, sshPublicKeys, *pgpSecrets)
	if err != nil {
		return nil, fmt.Errorf("find provider for git link %s: %w", repoURL, err)
	}

	// Cache the new provider
	m.providersToMRT[repoURL] = provider
	return provider, nil
}

func (m *Manager) providerExists(
	repoURL string,
) bool {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	return idx != -1
}

func (m *Manager) findProvider(
	ctx context.Context,
	repoURL, localPath string,
	auth transport.AuthMethod,
	pgpSecrets crypto.Secrets,
) (GitRepositoryProvider, error) {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	if idx == -1 {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}
	return m.providers[idx].New(ctx, repoURL, localPath, auth, pgpSecrets)
}
