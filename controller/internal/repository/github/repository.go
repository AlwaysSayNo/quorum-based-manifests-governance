// in internal/repository/provider_git.go
package github_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-logr/logr"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/internal/repository"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Helper struct for GetChangedFiles to parse Kind, Name, and Namespace
type k8sObjectMetadata struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
}

type GitProviderFactory struct {
}

// New creates and initializes a gitProvider
func (f *GitProviderFactory) New(ctx context.Context, remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets repository.PgpSecrets) (GitRepository, error) {
	p := &gitProvider{
		remoteURL: remoteURL,
		localPath: localPath,
		auth:      auth,
		logger:    log.FromContext(ctx),
	}
	// Sync on creation
	if err := p.Sync(context.Background()); err != nil {
		return nil, fmt.Errorf("initial sync failed: %w", err)
	}
	return p, nil
}

func (f *GitProviderFactory) IdentifyProvider(repoURL string) bool {
	return strings.Contains(repoURL, "github.com")
}

type GitRepository interface {
	Sync(ctx context.Context) error
	HasRevision(ctx context.Context, commit string) (bool, error)
	GetLatestRevision(ctx context.Context) (string, error)
	GetChangedFiles(ctx context.Context, fromCommit, toCommit string) ([]governancev1alpha1.FileChange, error)
	PushMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (string, error)
	PushSignature(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, governorAlias string, signatureData []byte) (string, error)
}

type gitProvider struct {
	remoteURL string
	localPath string
	repo      *git.Repository
	auth      transport.AuthMethod
	logger    logr.Logger
	// A mutex to protect repo from concurrent git operations
	mu         sync.Mutex
	pgpSecrets repository.PgpSecrets
}

// Sync ensures the local repository is cloned and up-to-date.
func (p *gitProvider) Sync(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Repo is already cloned. Pull the latest changes
	if p.repo != nil {
		w, err := p.repo.Worktree()
		if err != nil {
			return err
		}
		return w.PullContext(ctx, &git.PullOptions{
			RemoteName: "origin",
			Auth:       p.auth,
		})
	}

	// Check if a clone already exists on disk from a previous operator run.
	repo, err := git.PlainOpen(p.localPath)
	if err == nil {
		p.repo = repo
		// It exists, so just pull.
		return p.Sync(ctx)
	}

	// Local copy doesn't exist. Create it.
	repo, err = git.PlainCloneContext(ctx, p.localPath, false, &git.CloneOptions{
		URL:  p.remoteURL,
		Auth: p.auth,
	})
	if err != nil {
		return err
	}
	p.repo = repo
	return nil
}

func (p *gitProvider) HasRevision(ctx context.Context, commit string) (bool, error) {
	if err := p.Sync(ctx); err != nil {
		return false, fmt.Errorf("failed to sync repository before checking for revision: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Get the commit object for the HEAD of the default branch
	headRef, err := p.repo.Head()
	if err != nil {
		return false, fmt.Errorf("could not get HEAD reference: %w", err)
	}
	headCommit, err := p.repo.CommitObject(headRef.Hash())
	if err != nil {
		return false, fmt.Errorf("could not get HEAD commit object: %w", err)
	}

	// Get the commit object for the target commit
	targetHash := plumbing.NewHash(commit)
	targetCommit, err := p.repo.CommitObject(targetHash)
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("could not get target commit object %s: %w", commit, err)
	}

	// Check if the target commit is an ancestor of the head commit
	isAncestor, err := headCommit.IsAncestor(targetCommit)
	if err != nil {
		return false, fmt.Errorf("error checking ancestry for commit %s: %w", commit, err)
	}

	// Check if the target commit is the head commit
	isTheSame := headCommit.Hash.String() == targetHash.String()

	return isAncestor || isTheSame, nil
}

// GetLatestRevision takes head of the default branch and returns it's commit hash
func (p *gitProvider) GetLatestRevision(ctx context.Context) (string, error) {
	if err := p.Sync(ctx); err != nil {
		return "", fmt.Errorf("failed to sync repository before getting latest revision: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	headRef, err := p.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD reference: %w", err)
	}

	return headRef.Hash().String(), nil
}

func (p *gitProvider) GetChangedFiles(ctx context.Context, fromCommit, toCommit string) ([]governancev1alpha1.FileChange, error) {
	if err := p.Sync(ctx); err != nil {
		return nil, fmt.Errorf("failed to sync repository before getting changed files: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Get toTree for toCommit
	toTree, err := p.getTreeForCommit(ctx, toCommit)
	if err != nil {
		return nil, err
	}

	// Get fromTree for fromCommit
	var fromTree *object.Tree
	if fromCommit == "" {
		// Handle empty tree case
		fromTree = &object.Tree{}
	} else {
		fromTree, err = p.getTreeForCommit(ctx, fromCommit)
		if err != nil {
			return nil, err
		}
	}

	// Get patch (file difference) between commits
	patch, err := fromTree.Patch(toTree)
	if err != nil {
		return nil, fmt.Errorf("could not compute patch between commits: %w", err)
	}

	return p.patchToFileChangeList(toTree, patch)
}

func (p *gitProvider) getTreeForCommit(ctx context.Context, hash string) (*object.Tree, error) {
	hashObj := plumbing.NewHash(hash)
	commitObj, err := p.repo.CommitObject(hashObj)
	if err != nil {
		return nil, fmt.Errorf("could not find commit %s: %w", hash, err)
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return nil, fmt.Errorf("could not get tree for commit %s: %w", hash, err)
	}

	return tree, nil
}

func (p *gitProvider) patchToFileChangeList(toTree *object.Tree, patch *object.Patch) ([]governancev1alpha1.FileChange, error) {
	var changes []governancev1alpha1.FileChange

	for _, filePatch := range patch.FilePatches() {
		from, to := filePatch.Files()
		var path string
		var status governancev1alpha1.FileChangeStatus
		var file *object.File
		var err error

		if from == nil && to != nil {
			// File was added
			status = governancev1alpha1.New
			path = to.Path()
			file, err = toTree.File(path)
		} else if from != nil && to == nil {
			// File was deleted. No file to process
			status = governancev1alpha1.Deleted
			path = from.Path()
		} else if from != nil && to != nil {
			// File was modified
			status = governancev1alpha1.Updated
			path = to.Path()
			file, err = toTree.File(path)
		} else {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("could not get file object for path %s: %w", path, err)
		}

		// Get sha256 and meta
		var sha256Hex string
		var meta k8sObjectMetadata

		if file != nil {
			content, err := file.Contents()
			if err != nil {
				return nil, fmt.Errorf("could not read file contents for %s: %w", path, err)
			}

			// Calculate SHA256
			hasher := sha256.New()
			hasher.Write([]byte(content))
			sha256Hex = hex.EncodeToString(hasher.Sum(nil))

			if err := yaml.Unmarshal([]byte(content), &meta); err != nil {
				// Could be a non-k8s file, or malformed. Log and skip for now.
				p.logger.Error(err, fmt.Sprintf("could not unmarshal k8s metadata from %s, skipping: %v\n", path, err))
				continue
			}
		}

		changes = append(changes, governancev1alpha1.FileChange{
			Path:      path,
			Status:    status,
			Kind:      meta.Kind,
			Name:      meta.Metadata.Name,
			Namespace: meta.Metadata.Namespace,
			SHA256:    sha256Hex,
		})
	}

	return changes, nil
}

func (p *gitProvider) PushMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (string, error) {
	if err := p.Sync(ctx); err != nil {
		return "", fmt.Errorf("failed to sync repository before pushing MSR: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	worktree, err := p.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("could not get repository worktree: %w", err)
	}
	gpgEntity, err := p.getGpgEntity(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load GPG signing key: %w", err)
	}

	// Convert MSR object to file
	msrBytes, err := yaml.Marshal(msr)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MSR to YAML: %w", err)
	}
	// Create detached signature from the MSR file
	signatureBytes, err := createDetachedSignature(msrBytes, gpgEntity)
	if err != nil {
		return "", fmt.Errorf("failed to create detached signature for MSR: %w", err)
	}

	// Write MSR file into repo folder
	msrFileName := fmt.Sprintf("%s.yaml", msr.Name)
	sigFileName := fmt.Sprintf("%s.yaml.sig", msr.Name)

	msrFilePath := filepath.Join(p.localPath, msr.Spec.Location.Folder, msrFileName)
	sigFilePath := filepath.Join(p.localPath, msr.Spec.Location.Folder, sigFileName)

	msrRepoPath := filepath.Join(msr.Spec.Location.Folder, msrFileName)
	sigRepoPath := filepath.Join(msr.Spec.Location.Folder, sigFileName)

	err = os.WriteFile(msrFilePath, msrBytes, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write MSR file to worktree: %w", err)
	}
	if err := os.WriteFile(sigFilePath, signatureBytes, 0644); err != nil {
		return "", fmt.Errorf("failed to write signature file: %w", err)
	}

	// Function to delete MSR file, if error appears
	deleteFile := func() {
		os.Remove(msrFileName)
		os.Remove(sigFilePath)
	}
	// Function to rollback working tree to the old state, if error appears
	headRef, err := p.repo.Head()
	if err != nil {
		return "", fmt.Errorf("could not get current HEAD before commit: %w", err)
	}
	originalCommitHash := headRef.Hash()
	rollback := func() {
		worktree.Reset(&git.ResetOptions{
			Commit: originalCommitHash,
			Mode:   git.HardReset,
		})
	}

	// Add files to the staging area
	if _, err = worktree.Add(msrRepoPath); err != nil {
		rollback()
		deleteFile()
		return "", fmt.Errorf("failed to git add MSR file to the staging area: %w", err)
	}
	if _, err := worktree.Add(sigRepoPath); err != nil {
		rollback()
		return "", fmt.Errorf("failed to git add signature file: %w", err)
	}

	// Commit and push changes
	commitMsg := fmt.Sprintf("chore(governance): create manifest signing request %s with version %d", msr.Name, msr.Spec.Version)
	commitOpts := &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Governance Operator",
			Email: "operator@yourdomain.com",
			When:  time.Now(),
		},
		SignKey: gpgEntity,
	}
	commitHash, err := worktree.Commit(commitMsg, commitOpts)
	if err != nil {
		rollback()
		deleteFile()
		return "", fmt.Errorf("failed to commit MSR: %w", err)
	}

	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		Auth:       p.auth,
	}
	err = p.repo.PushContext(ctx, pushOpts)
	if err != nil {
		rollback()
		deleteFile()
		return "", fmt.Errorf("failed to push MSR commit: %w", err)
	}

	return commitHash.String(), nil
}

func (p *gitProvider) PushSignature(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, governorAlias string, signatureData []byte) (string, error) {
	return "", nil
}

func (p *gitProvider) getGpgEntity(ctx context.Context) (*openpgp.Entity, error) {
	if p.pgpSecrets.PgpKey == "" {
		return nil, fmt.Errorf("PGP private key is not configured")
	}

	entityList, err := openpgp.ReadArmoredKeyRing(strings.NewReader(p.pgpSecrets.PgpKey))
	if err != nil {
		return nil, err
	}
	entity := entityList[0]

	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		// If a passphrase is required but not provided, fail.
		if p.pgpSecrets.PgpPassphrase == "" {
			return nil, fmt.Errorf("PGP private key is encrypted, but no passphrase was provided")
		}

		passphrase := []byte(p.pgpSecrets.PgpPassphrase)

		// Attempt to decrypt the private key.
		err := entity.PrivateKey.Decrypt(passphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt PGP private key: %w", err)
		}
	}

	return entity, nil
}

// createDetachedSignature takes the raw bytes of a file and a GPG entity,
// and returns the raw bytes of an armored, detached signature.
func createDetachedSignature(fileContent []byte, signKey *openpgp.Entity) ([]byte, error) {
	// Create a buffer to hold the armored signature
	sigBuf := new(bytes.Buffer)
	armorWriter, err := armor.Encode(sigBuf, openpgp.SignatureType, nil)
	if err != nil {
		return nil, err
	}

	// Generate the detached signature from the file content
	err = openpgp.DetachSign(armorWriter, signKey, bytes.NewReader(fileContent), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create detached signature: %w", err)
	}
	armorWriter.Close()

	return sigBuf.Bytes(), nil
}
