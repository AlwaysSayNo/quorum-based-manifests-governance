package github

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

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-logr/logr"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/controller-runtime/pkg/log"

	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
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
func (f *GitProviderFactory) New(ctx context.Context, remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets manager.Secrets) (manager.GitRepository, error) {
	p := &gitProvider{
		remoteURL:  remoteURL,
		localPath:  localPath,
		auth:       auth,
		logger:     log.FromContext(ctx),
		pgpSecrets: pgpSecrets,
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

type gitProvider struct {
	remoteURL string
	localPath string
	repo      *git.Repository
	auth      transport.AuthMethod
	logger    logr.Logger
	// A mutex to protect repo from concurrent git operations
	mu         sync.Mutex
	pgpSecrets manager.Secrets
}

// Sync ensures the local repository is cloned and up-to-date.
func (p *gitProvider) Sync(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check, if repo is not cloned
	if p.repo == nil {
		// Check if a clone already exists on disk from a previous operator run
		repo, err := git.PlainOpen(p.localPath)
		if err != nil {
			// Local copy doesn't exist. Create it
			repo, err = git.PlainCloneContext(ctx, p.localPath, false, &git.CloneOptions{
				URL:  p.remoteURL,
				Auth: p.auth,
			})
			if err != nil {
				return err
			}
		}
		p.repo = repo
	}

	// Repo is already cloned. Pull the latest changes
	w, err := p.repo.Worktree()
	if err != nil {
		return err
	}
	err = w.PullContext(ctx, &git.PullOptions{
		RemoteName: "origin",
		Auth:       p.auth,
	})

	if err != nil && err != git.NoErrAlreadyUpToDate {
		return err
	}
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
	isAncestor, err := targetCommit.IsAncestor(headCommit)
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

func (p *gitProvider) GetChangedFiles(ctx context.Context, fromCommit, toCommit string, fromFolder string) ([]manager.FileChange, error) {
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

	return p.patchToFileChangeList(fromTree, toTree, patch, fromFolder)
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

func (p *gitProvider) patchToFileChangeList(fromTree *object.Tree, toTree *object.Tree, patch *object.Patch, fromFolder string) ([]manager.FileChange, error) {
	var changes []manager.FileChange
	fromFolderNormalized := filepath.Clean(fromFolder)

	for _, filePatch := range patch.FilePatches() {
		from, to := filePatch.Files()
		var path string
		var status manager.FileChangeStatus
		var file *object.File
		var err error

		if from == nil && to != nil {
			// File was added
			status = manager.New
			path = to.Path()
			file, err = toTree.File(path)
		} else if from != nil && to == nil {
			// File was deleted. Take old file information
			status = manager.Deleted
			path = from.Path()
			file, err = fromTree.File(path)
		} else if from != nil && to != nil {
			// File was modified
			status = manager.Updated
			path = to.Path()
			file, err = toTree.File(path)
		} else {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("could not get file object for path %s: %w", path, err)
		}

		if !strings.HasPrefix(path, fromFolderNormalized+"/") {
			continue
		}

		// Get sha256 and meta
		var sha256Hex string
		var meta k8sObjectMetadata

		if file != nil {
			content, err := file.Contents()
			if err != nil {
				return nil, fmt.Errorf("could not read file contents for %s: %w", path, err)
			}

			if err := yaml.Unmarshal([]byte(content), &meta); err != nil {
				// Could be a non-k8s file, or malformed. Log and skip for now.
				p.logger.Error(err, fmt.Sprintf("could not unmarshal k8s metadata from %s, skipping: %v\n", path, err))
				continue
			}

			// Calculate SHA256
			hasher := sha256.New()
			hasher.Write([]byte(content))
			sha256Hex = hex.EncodeToString(hasher.Sum(nil))
		}

		changes = append(changes, manager.FileChange{
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

func (p *gitProvider) PushMSR(ctx context.Context, msr *manager.ManifestSigningRequestManifestObject) (string, error) {
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

	// Create folder for new MSR and signatures subfolder
	repoRequestFolderPath := filepath.Join(msr.Spec.Location.Folder, fmt.Sprintf("v_%d", msr.Spec.Version))
	repoMsrSignaturesFolderPath := filepath.Join(repoRequestFolderPath, "signatures")

	os.MkdirAll(filepath.Join(p.localPath, repoRequestFolderPath), 0644)
	os.MkdirAll(filepath.Join(p.localPath, repoMsrSignaturesFolderPath), 0644)

	// Write MSR and sig files into repo folder
	msrFileName := fmt.Sprintf("%s.yaml", msr.ObjectMeta.Name)
	sigFileName := fmt.Sprintf("%s.yaml.sig", msr.ObjectMeta.Name)

	if err := p.addCreateFileAndAddToWorktree(worktree, repoRequestFolderPath, msrFileName, msrBytes); err != nil {
		rollback()
		return "", fmt.Errorf("create MSR file and to worktree: %w", err)
	}
	if err := p.addCreateFileAndAddToWorktree(worktree, repoRequestFolderPath, sigFileName, signatureBytes); err != nil {
		rollback()
		return "", fmt.Errorf("create MSR.sig file and to worktree: %w", err)
	}

	// Commit and push changes
	commitMsg := fmt.Sprintf("New ManifestSigningRequest: create manifest signing request %s with version %d", msr.ObjectMeta.Name, msr.Spec.Version)
	commitOpts := &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Qubmango Governance Operator",
			Email: "noreply@qubmango.com",
			When:  time.Now(),
		},
		SignKey: gpgEntity,
	}
	commitHash, err := worktree.Commit(commitMsg, commitOpts)
	if err != nil {
		rollback()
		return "", fmt.Errorf("failed to commit MSR: %w", err)
	}

	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		Auth:       p.auth,
	}
	err = p.repo.PushContext(ctx, pushOpts)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		rollback()
		return "", fmt.Errorf("failed to push MSR commit: %w", err)
	}

	return commitHash.String(), nil
}

func (p *gitProvider) addCreateFileAndAddToWorktree(worktree *git.Worktree, repoFolderPath, fileName string, file []byte) error {
	filePath := filepath.Join(p.localPath, repoFolderPath, fileName)
	repoPath := filepath.Join(repoFolderPath, fileName)

	err := os.WriteFile(filePath, file, 0644)
	if err != nil {
		return fmt.Errorf("failed to write MSR file: %w", err)
	}

	if _, err = worktree.Add(repoPath); err != nil {
		return fmt.Errorf("failed to git add MSR file to the staging area: %w", err)
	}

	return nil
}

func (p *gitProvider) PushSignature(ctx context.Context, msr *manager.ManifestSigningRequestManifestObject, governorAlias string, signatureData []byte) (string, error) {
	return "", nil
}

func (p *gitProvider) getGpgEntity(ctx context.Context) (*openpgp.Entity, error) {
	if p.pgpSecrets.PrivateKey == "" {
		return nil, fmt.Errorf("PGP private key is not configured")
	}

	entityList, err := openpgp.ReadArmoredKeyRing(strings.NewReader(p.pgpSecrets.PrivateKey))
	if err != nil {
		return nil, err
	}
	entity := entityList[0]

	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		// If a passphrase is required but not provided, fail.
		if p.pgpSecrets.Passphrase == "" {
			return nil, fmt.Errorf("PGP private key is encrypted, but no passphrase was provided")
		}

		passphrase := []byte(p.pgpSecrets.Passphrase)

		// Attempt to decrypt the private key.
		err := entity.PrivateKey.Decrypt(passphrase)
		if err != nil || entity.PrivateKey.Encrypted {
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
