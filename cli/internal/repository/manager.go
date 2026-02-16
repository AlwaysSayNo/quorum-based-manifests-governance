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

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	crypto "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/crypto"
)

const (
	// DefaultReposPath is the default directory for storing local Git repository clones
	DefaultReposPath = "/tmp/qubmango/git/repos"
)

// MCAInfo holds information about a ManifestChangeApproval.
type MCAInfo struct {
	Obj     *dto.ManifestChangeApprovalManifestObject
	Content []byte
	Sign    dto.SignatureData
}

// MSRInfo holds information about a ManifestSigningRequest including.
type MSRInfo struct {
	Obj            *dto.ManifestSigningRequestManifestObject
	Content        []byte
	Sign           dto.SignatureData
	GovernorsSigns []dto.SignatureData
}

type GovernancePolicy struct {
	GovernancePath string
	MSRName        string
	MCAName        string
}

func NewMyFilePatch(
	filePatch []diff.FilePatch,
	message string,
) *myFilePatch {
	return &myFilePatch{
		filePatch: filePatch,
		message:   message,
	}
}

// myFilePatch is a helper struct that implements the diff.Patch interface.
// This allows passing an arbitrary subset of FilePatches to the encoder.
type myFilePatch struct {
	// filePatch is the list of file patches
	filePatch []diff.FilePatch
	// message is the commit message associated with this patch
	message string
}

// This method implements the diff.Patch interface.
func (mfp *myFilePatch) FilePatches() []diff.FilePatch {
	if mfp.filePatch == nil {
		return nil
	}
	return mfp.filePatch
}

// This method implements the diff.Patch interface.
func (mfp *myFilePatch) Message() string {
	return mfp.message
}

// GitRepositoryFactory creates new GitRepositoryProvider instances for specific Git hosting providers
type GitRepositoryFactory interface {
	New(ctx context.Context, remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets crypto.Secrets) (GitRepositoryProvider, error)
	IdentifyProvider(repoURL string) bool
}

// GitRepositoryProvider defines the interface for interacting with a Git repository
type GitRepositoryProvider interface {
	// Sync ensures the local clone of the repository is up-to-date with the remote.
	// This should be called periodically and before any read/write operations.
	Sync(ctx context.Context) error

	// HasRevision returns true if the revision commit is part of the git repository.
	HasRevision(ctx context.Context, commit string) (bool, error)

	// GetLatestRevision returns the last observed revision in the repository.
	GetLatestRevision(ctx context.Context) (string, error)

	// GetChangedFilesRaw returns a map of changed files between two commits with their raw content and status.
	GetChangedFilesRaw(ctx context.Context, fromCommit, toCommit string, fromFolder string) (map[string]dto.FileBytesWithStatus, error)

	// PushGovernorSignature commits and pushes a governor's signature to the repository.
	PushGovernorSignature(ctx context.Context, msr *dto.ManifestSigningRequestManifestObject, user config.UserInfo) (string, error)

	// GetLatestMSR retrieves the latest MSR manifest from the repository along with signatures.
	GetLatestMSR(ctx context.Context, policy *GovernancePolicy) (*MSRInfo, error)

	// GetMCAHistory retrieves the history of all MCA approvals from the repository.
	GetMCAHistory(ctx context.Context, policy *GovernancePolicy) ([]MCAInfo, error)

	// GetFileDiffPatchParts returns diff patches for files changed between two commits, grouped by file.
	GetFileDiffPatchParts(ctx context.Context, msr *dto.ManifestSigningRequestManifestObject, fromCommit, toCommit string) (map[string]diff.Patch, error)
}

// Manager handles the lifecycle of different repository provider instances.
// It maintains a cache of providers for each repository URL.
type Manager struct {
	// basePath is the base directory to store local clones
	basePath string
	// providers is the list of all available provider factories
	providers []GitRepositoryFactory
	// providersToMRT is the cache of initialized providers, keyed by repo URL
	providersToMRT map[string]GitRepositoryProvider
	// mu protects concurrent access to the manager's state
	mu sync.Mutex
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
	KnownHostsPath   string
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
	sshPublicKeys, err := crypto.SyncSSHSecrets(ctx, sshSecrets, conf.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("sync ssh secrets: %w", err)
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
