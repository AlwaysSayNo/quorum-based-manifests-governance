package repository

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

type GitRepositoryFactory interface {
	New(ctx context.Context, remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets PgpSecrets) (GitRepository, error)
	IdentifyProvider(repoURL string) bool
}

type GitRepository interface {
	// Sync ensures the local clone of the repository is up-to-date with the remote.
	// This should be called periodically and before any read/write operations.
	Sync(ctx context.Context) error

	// HasRevision return true, if revision commit is the part of git repository.
	HasRevision(ctx context.Context, commit string) (bool, error)

	IsNotAfter(ctx context.Context, ancestor, child string) (bool, error)

	// GetLatestRevision return the last observed revision for the repository.
	GetLatestRevision(ctx context.Context) (string, error)

	// GetChangedFiles returns a list of files that changed between two commits and a map of their path to content.
	GetChangedFiles(ctx context.Context, fromCommit, toCommit string, fromFolder string) ([]governancev1alpha1.FileChange, map[string]string, error)

	// PushMSR commits and pushes the generated MSR manifest to the correct folder in the repo along with its signature.
	PushMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequestManifestObject) (string, error)

	// PushMCA commits and pushes the generated MCA manifest to the correct folder in the repo along with its signature.
	PushMCA(ctx context.Context, msr *governancev1alpha1.ManifestChangeApprovalManifestObject) (string, error)

	// PushGovernorSignature commits and pushes a qubmango's as a governor signature to the repository.
	PushGovernorSignature(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequestManifestObject) (string, error)

	FetchMSRByVersion(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (*dto.ManifestSigningRequestManifestObject, []byte, dto.SignatureData, []dto.SignatureData, error)

	GetRemoteHeadCommit(ctx context.Context) (string, error)

	GetLocalHeadCommit(ctx context.Context) (string, error)

	PushSummaryFile(ctx context.Context, content, fileName, toFolder string, version int) (string, error)

	// DeleteFolder deletes a folder from the repository and pushes to remote.
	DeleteFolder(ctx context.Context, folderPath string) error
}

type PgpSecrets struct {
	PrivateKey string
	Passphrase string
}

// Manager handles the lifecycle of different repository provider instances.
// It acts as a proxy to the underlying providers by caching secrets with TTL
// and refreshing them periodically to handle secret rotation.
type Manager struct {
	client client.Client
	// Base directory to store local clones
	basePath string
	// Holds location of known_hosts
	knownHostsPath string
	// List of all available providers factories
	providers []GitRepositoryFactory
	// Cache of initialized providersToMRT, keyed by repo URL
	providersToMRT map[string]GitRepository
	// Cache of MRT references for each provider, keyed by repo URL
	mrtReferences map[string]*governancev1alpha1.ManifestRequestTemplate
	// lastSecretRefresh tracks when secrets were last refreshed per repo URL
	lastSecretRefresh map[string]int64
	// secretRefreshTTL is the time-to-live for cached secrets in seconds
	secretRefreshTTL int64
	mu               sync.Mutex
	logger           logr.Logger
}

func NewManager(
	client client.Client,
	basePath string,
	knownHostsPath string,
) *Manager {
	return &Manager{
		client:            client,
		basePath:          basePath,
		knownHostsPath:    knownHostsPath,
		providers:         []GitRepositoryFactory{},
		providersToMRT:    make(map[string]GitRepository),
		mrtReferences:     make(map[string]*governancev1alpha1.ManifestRequestTemplate),
		lastSecretRefresh: make(map[string]int64),
		secretRefreshTTL:  600, // 10 minutes default
		logger:            log.FromContext(context.Background()).WithName("git-manager"),
	}
}

func (m *Manager) Register(
	factory GitRepositoryFactory,
) error {
	m.providers = append(m.providers, factory)
	return nil
}

func (m *Manager) GetProviderForMRT(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (GitRepository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoURL := mrt.Spec.GitRepository.SSH.URL
	if repoURL == "" {
		return nil, fmt.Errorf("repository URL is not defined in MRT spec")
	}
	if !m.providerExists(repoURL) {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}

	// Check if we already have a provider for this URL in our cache
	if provider, ok := m.providersToMRT[repoURL]; ok {
		// Refresh secrets if TTL expired
		m.refreshSecretsIfExpired(ctx, repoURL)
		return provider, nil
	}

	// Generate unique local path for the clone from the repo URL
	repoHash := fmt.Sprintf("%x", sha1.Sum([]byte(repoURL)))
	localPath := filepath.Join(m.basePath, repoHash)

	// Sync pgp and ssh secrets
	pgpSecrets, err := m.syncPGPSecrets(ctx, mrt)
	if err != nil {
		return nil, err
	}
	sshSecrets, err := m.syncSSHSecrets(ctx, mrt)
	if err != nil {
		return nil, err
	}

	provider, err := m.findProvider(ctx, repoURL, localPath, sshSecrets, pgpSecrets)
	if err != nil {
		return nil, err
	}

	// Cache the new provider and MRT reference
	m.providersToMRT[repoURL] = provider
	m.mrtReferences[repoURL] = mrt
	m.lastSecretRefresh[repoURL] = m.getCurrentTimestamp()
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
	pgpSecrets PgpSecrets,
) (GitRepository, error) {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	if idx == -1 {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}
	return m.providers[idx].New(ctx, repoURL, localPath, auth, pgpSecrets)
}

func (m *Manager) syncPGPSecrets(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (PgpSecrets, error) {
	if mrt.Spec.PGP == nil {
		return PgpSecrets{}, fmt.Errorf("pgp information is nil")
	}

	pgpSecret := &corev1.Secret{}
	err := m.client.Get(ctx, types.NamespacedName{Name: mrt.Spec.PGP.SecretsRef.Name, Namespace: mrt.Spec.PGP.SecretsRef.Namespace}, pgpSecret)
	if err != nil {
		return PgpSecrets{}, fmt.Errorf("failed to fetch pgp secret: %w", err)
	}

	privateKeyBytes, ok := pgpSecret.Data["privateKey"]
	if !ok {
		return PgpSecrets{}, fmt.Errorf("secret '%s' is missing 'privateKey' field", mrt.Spec.PGP.SecretsRef.Name)
	}

	passphraseBytes, ok := pgpSecret.Data["passphrase"]
	if !ok {
		// If the key is passphrase-protected, this will cause the next step to fail.
		// We can treat it as an empty string and let the crypto library handle the failure
		passphraseBytes = []byte("")
	}

	return PgpSecrets{
		PrivateKey: string(privateKeyBytes),
		Passphrase: string(passphraseBytes),
	}, nil
}

func (m *Manager) syncSSHSecrets(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (*ssh.PublicKeys, error) {
	if mrt.Spec.GitRepository.SSH.SecretsRef == nil {
		return nil, fmt.Errorf("ssh information is nil")
	}

	secretRef := mrt.Spec.GitRepository.SSH.SecretsRef
	gitSecret := &corev1.Secret{}
	err := m.client.Get(ctx, types.NamespacedName{Name: secretRef.Name, Namespace: secretRef.Namespace}, gitSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch git secret '%s': %w", secretRef.Name, err)
	}

	privateKeyBytes, ok := gitSecret.Data["ssh-privatekey"]
	if !ok {
		return nil, fmt.Errorf("secret '%s' is missing 'privateKey' field", secretRef.Name)
	}

	passphraseBytes, ok := gitSecret.Data["passphrase"]
	if !ok {
		// If the key is passphrase-protected, this will cause the next step to fail.
		// We can treat it as an empty string and let the crypto library handle the failure.
		passphraseBytes = []byte("")
	}

	// Check the existence of the knownHostsPath
	if _, err := os.Stat(m.knownHostsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("known_hosts file does not exist at the expected path: %s. Check the deployment's volume mounts", m.knownHostsPath)
	}

	// Create callback for hostPaths
	hostKeyCallback, err := knownhosts.New(m.knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create known_hosts callback from path '%s': %w", m.knownHostsPath, err)
	}

	// Create the public keys object
	publicKeys, err := ssh.NewPublicKeys("git", privateKeyBytes, string(passphraseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create public keys from secret: %w", err)
	}

	// Attach callback
	publicKeys.HostKeyCallback = hostKeyCallback

	return publicKeys, nil
}

// refreshProviderSecrets refreshes the SSH and PGP secrets for a provider by fetching them from Kubernetes.
// This ensures that rotated secrets are always used for git operations.
func (m *Manager) refreshProviderSecrets(
	ctx context.Context,
	repoURL string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mrt, ok := m.mrtReferences[repoURL]
	if !ok {
		return fmt.Errorf("MRT reference not found for repository URL: %s", repoURL)
	}

	provider, ok := m.providersToMRT[repoURL]
	if !ok {
		return fmt.Errorf("provider not found for repository URL: %s", repoURL)
	}

	// Fetch fresh secrets
	sshSecrets, err := m.syncSSHSecrets(ctx, mrt)
	if err != nil {
		return fmt.Errorf("failed to refresh SSH secrets: %w", err)
	}

	pgpSecrets, err := m.syncPGPSecrets(ctx, mrt)
	if err != nil {
		return fmt.Errorf("failed to refresh PGP secrets: %w", err)
	}

	// Update provider with fresh credentials
	type providerWithSecrets interface {
		UpdateSecrets(auth transport.AuthMethod, pgpSecrets PgpSecrets) error
	}

	if updatable, ok := provider.(providerWithSecrets); ok {
		if err := updatable.UpdateSecrets(sshSecrets, pgpSecrets); err != nil {
			return fmt.Errorf("failed to update provider secrets: %w", err)
		}
	}

	return nil
}

// refreshSecretsIfExpired checks if secrets for a provider have expired based on TTL.
// Errors are logged but don't block.
func (m *Manager) refreshSecretsIfExpired(
	ctx context.Context,
	repoURL string,
) {
	lastRefresh, exists := m.lastSecretRefresh[repoURL]
	currentTime := m.getCurrentTimestamp()

	// Check, if never refreshed or TTL expired
	if !exists || (currentTime-lastRefresh) > m.secretRefreshTTL {
		if err := m.refreshProviderSecrets(ctx, repoURL); err != nil {
			m.logger.Error(err, "Failed to refresh provider secrets")
			return
		}
		m.lastSecretRefresh[repoURL] = currentTime
	}
}

func (m *Manager) getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

// SetSecretRefreshTTL allows configuring the TTL for secret caching.
func (m *Manager) SetSecretRefreshTTL(
	ttlSeconds int64,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secretRefreshTTL = ttlSeconds
}
