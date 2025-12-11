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
	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (m *Manager) GetProviderForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (GitRepository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	application, err := m.getApplication(ctx, mrt)
	if err != nil {
		return nil, err
	}

	repoURL := application.Spec.Source.RepoURL
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
	pgpSecrets, err := m.syncPGPSecrets(ctx, mrt)
	if err != nil {
		return nil, err
	}
	sshSecrets, err := m.syncSSHSecrets(ctx, mrt)
	if err != nil {
		return nil, err
	}

	provider, err := m.findProvider(repoURL, localPath, sshSecrets, pgpSecrets)
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

func (m *Manager) findProvider(repoURL, localPath string, auth transport.AuthMethod, pgpSecrets PgpSecrets) (GitRepository, error) {
	idx := slices.IndexFunc(m.providers, func(provider GitRepositoryFactory) bool {
		return provider.IdentifyProvider(repoURL)
	})

	if idx == -1 {
		return nil, fmt.Errorf("no supported git provider for URL: %s", repoURL)
	}
	return m.providers[idx].New(repoURL, localPath, auth, pgpSecrets)
}

func (m *Manager) syncPGPSecrets(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (PgpSecrets, error) {
	// TODO: Decide between direct PGP key usage in MRT or as ref
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
		// We can treat it as an empty string and let the crypto library handle the failure.
		passphraseBytes = []byte("")
	}

	return PgpSecrets{
		PgpKey:        string(privateKeyBytes),
		PgpPassphrase: string(passphraseBytes),
	}, nil
}

func (m *Manager) syncSSHSecrets(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*ssh.PublicKeys, error) {
	// TODO: Decide between direct SHH key usage in MRT or as ref
	gitSecret := &corev1.Secret{}
	err := m.client.Get(ctx, types.NamespacedName{Name: mrt.Spec.SSH.SecretsRef.Name, Namespace: mrt.Spec.SSH.SecretsRef.Namespace}, gitSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch git secret '%s': %w", mrt.Spec.SSH.SecretsRef.Name, err)
	}

	privateKeyBytes, ok := gitSecret.Data["privateKey"]
	if !ok {
		return nil, fmt.Errorf("secret '%s' is missing 'privateKey' field", mrt.Spec.SSH.SecretsRef.Name)
	}

	passphraseBytes, ok := gitSecret.Data["passphrase"]
	if !ok {
		// If the key is passphrase-protected, this will cause the next step to fail.
		// We can treat it as an empty string and let the crypto library handle the failure.
		passphraseBytes = []byte("")
	}

	// Decrypt the private key.
	publicKeys, err := ssh.NewPublicKeys("git", privateKeyBytes, string(passphraseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create public keys from secret: %w", err)
	}

	return publicKeys, nil
}

// getApplication fetches the ArgoCD Application resource referenced by the ManifestRequestTemplate
func (m *Manager) getApplication(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*argocdv1alpha1.Application, error) {
	// TODO: create a defaulting webhook to set these values, in order to avoid validating it every time.
	appNamespace := mrt.Spec.ArgoCDApplication.Namespace
	if appNamespace == "" {
		appNamespace = "argocd"
	}

	app := &argocdv1alpha1.Application{}
	appKey := types.NamespacedName{
		Name:      mrt.Spec.ArgoCDApplication.Name,
		Namespace: appNamespace,
	}

	if err := m.client.Get(ctx, appKey, app); err != nil {
		// TODO: create validating webhook, that ensures, that MRT corresponds to an existing Application
		// TODO: In validating webhook we can also set mapping for Application to MRT (in case of non default Application namespace)
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("no Application for MRT was found")
		}
		return nil, err
	}

	return app, nil
}
