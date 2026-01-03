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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-logr/logr"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

type fileToPush struct {
	Object  interface{}
	Name    string
	Folder  string
	Version int
}

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
func (f *GitProviderFactory) New(
	ctx context.Context,
	remoteURL, localPath string,
	auth transport.AuthMethod,
	pgpSecrets repository.PgpSecrets,
) (repository.GitRepository, error) {
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

func (f *GitProviderFactory) IdentifyProvider(
	repoURL string,
) bool {
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
	pgpSecrets repository.PgpSecrets
}

// Sync ensures the local repository is cloned and up-to-date.
func (p *gitProvider) Sync(
	ctx context.Context,
) error {
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

func (p *gitProvider) HasRevision(
	ctx context.Context,
	commit string,
) (bool, error) {
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

func (p *gitProvider) IsNotAfter(
	ctx context.Context,
	ancestor, child string,
) (bool, error) {
	if err := p.Sync(ctx); err != nil {
		return false, fmt.Errorf("failed to sync repository before checking for revision: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Get the commit object for the ancestor commit
	ancestorHash := plumbing.NewHash(ancestor)
	ancestorCommit, err := p.repo.CommitObject(ancestorHash)
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("could not get ancestor commit object %s: %w", ancestor, err)
	}

	// Get the commit object for the child commit
	childHash := plumbing.NewHash(child)
	childCommit, err := p.repo.CommitObject(childHash)
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("could not get child commit object %s: %w", child, err)
	}

	// Check if the target ancestorCommit is an ancestor of the childCommit
	isChild, err := ancestorCommit.IsAncestor(childCommit)
	if err != nil {
		return false, fmt.Errorf("error checking ancestry for %s and %s: %w", ancestor, child, err)
	}

	// Check if the ancestorCommit is childCommit
	isTheSame := ancestorCommit.Hash.String() == childCommit.String()

	return !isChild || isTheSame, nil
}

// GetLatestRevision takes head of the default branch and returns it's commit hash
func (p *gitProvider) GetLatestRevision(
	ctx context.Context,
) (string, error) {
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

func (p *gitProvider) GetChangedFiles(
	ctx context.Context,
	fromCommit, toCommit string,
	fromFolder string,
) ([]governancev1alpha1.FileChange, error) {
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

func (p *gitProvider) getTreeForCommit(
	ctx context.Context,
	hash string,
) (*object.Tree, error) {
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

func (p *gitProvider) patchToFileChangeList(
	fromTree *object.Tree,
	toTree *object.Tree,
	patch *object.Patch,
	fromFolder string,
) ([]governancev1alpha1.FileChange, error) {
	var changes []governancev1alpha1.FileChange
	fromFolderNormalized := filepath.Clean(fromFolder)

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
			// File was deleted. Take old file information
			status = governancev1alpha1.Deleted
			path = from.Path()
			file, err = fromTree.File(path)
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

func (p *gitProvider) PushMSR(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequestManifestObject,
) (string, error) {
	files := []fileToPush{
		{Object: msr, Name: msr.ObjectMeta.Name, Folder: msr.Spec.Locations.GovernancePath, Version: msr.Spec.Version},
	}
	msg := fmt.Sprintf("New ManifestSigningRequest: create manifest signing request %s with version %d", msr.ObjectMeta.Name, msr.Spec.Version)
	return p.pushWorkflow(ctx, files, msg)
}

func (p *gitProvider) PushMCA(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApprovalManifestObject,
) (string, error) {
	files := []fileToPush{
		{Object: mca, Name: mca.ObjectMeta.Name, Folder: mca.Spec.Locations.GovernancePath, Version: mca.Spec.Version},
	}
	msg := fmt.Sprintf("New ManifestChangeApproval: create manifest change approval %s with version %d", mca.ObjectMeta.Name, mca.Spec.Version)
	return p.pushWorkflow(ctx, files, msg)
}

func (p *gitProvider) RemoveFromIndexFile(
	ctx context.Context,
	operationalFileLocation,
	governanceIndexAlias string,
) (string, bool, error) {
	worktree, rollback, gpgEntity, err := p.syncAndLock(ctx)
	if err != nil {
		return "", false, fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	fullOperationalFilePath := filepath.Join(p.localPath, operationalFileLocation)

	// Check correctness of operational file name (a yaml file with non-empty name)
	fileName := filepath.Base(fullOperationalFilePath)
	if !strings.HasSuffix(fileName, ".yaml") || fileName == ".yaml" {
		return "", false, fmt.Errorf("incorrect operational .yaml file name %s", fileName)
	}

	if _, err := os.Stat(fullOperationalFilePath); os.IsNotExist(err) {
		return "", false, fmt.Errorf("index file doesn't exist in %s", fullOperationalFilePath)
	}

	// File exists - read and unmarshal
	fileBytes, err := os.ReadFile(fullOperationalFilePath)
	if err != nil {
		return "", false, fmt.Errorf("read qubmango index file: %w", err)
	}

	var indexFile governancev1alpha1.QubmangoIndex
	if err := yaml.Unmarshal(fileBytes, &indexFile); err != nil {
		return "", false, fmt.Errorf("unmarshal qubmango index file: %w", err)
	}

	// Check, if index with such alias already exist. Alias should be unique
	idx := slices.IndexFunc(indexFile.Spec.Policies, func(policy governancev1alpha1.QubmangoPolicy) bool {
		return policy.Alias == governanceIndexAlias
	})
	if idx == -1 {
		// Index didn't exist - do nothing and return false (no index existed before).
		return "", false, nil
	}

	// Remove existing policy.
	indexFile.Spec.Policies = append(indexFile.Spec.Policies[:idx], indexFile.Spec.Policies[idx+1:]...)

	// Write back indexFile to filesystem
	updatedFileBytes, err := yaml.Marshal(indexFile)
	if err != nil {
		return "", false, fmt.Errorf("marshal updated index file: %w", err)
	}

	if err := os.WriteFile(fullOperationalFilePath, updatedFileBytes, 0644); err != nil {
		return "", false, fmt.Errorf("write updated index file: %w", err)
	}

	// Add the modified index file to the staging tree
	if _, err = worktree.Add(operationalFileLocation); err != nil {
		return "", false, fmt.Errorf("failed to git add qubmango index file to the staging area: %w", err)
	}

	// Created signed commit and push it to the remote repo
	commitMsg := fmt.Sprintf("create entry in .governance/index.yaml file for %s", governanceIndexAlias)
	commitHash, err := p.commitAndPush(ctx, worktree, commitMsg, gpgEntity)
	if err != nil {
		rollback()
		return "", false, fmt.Errorf("commit and push: %w", err)
	}

	return commitHash, true, nil
}

func (p *gitProvider) PushGovernorSignature(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequestManifestObject,
) (string, error) {
	worktree, rollback, pgpEntity, err := p.syncAndLock(ctx)
	if err != nil {
		return "", fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	// Sign MSR as a governor and add it to the governance signatures folder in the worktree
	if err := p.createFileGovernorSignatureAndAttach(worktree, pgpEntity, msr, msr.ObjectMeta.Name, msr.Spec.Locations.GovernancePath, msr.Spec.Version); err != nil {
		rollback()
		return "", fmt.Errorf("stage governor signature: %w", err)
	}

	// Created signed commit and push it to the remote repo
	commitMsg := fmt.Sprintf("Qubmango as governor signature for ManifestSigningRequest: create manifest signing request %s with version %d", msr.ObjectMeta.Name, msr.Spec.Version)
	commitHash, err := p.commitAndPush(ctx, worktree, commitMsg, pgpEntity)
	if err != nil {
		rollback()
		return "", fmt.Errorf("commit and push: %w", err)
	}

	return commitHash, nil
}

func (p *gitProvider) createFileGovernorSignatureAndAttach(
	worktree *git.Worktree,
	pgpEntity *openpgp.Entity,
	file any,
	fileName, inRepoFolderPath string,
	version int,
) error {
	// Create signatures folder.
	repoSignaturesFolderPath := filepath.Join(inRepoFolderPath, fmt.Sprintf("v_%d", version), "signatures")
	os.MkdirAll(filepath.Join(p.localPath, repoSignaturesFolderPath), 0644)

	// Convert publicKey into a hash.
	pubKeyHash, err := p.convertPublicKeyToHash(pgpEntity)
	if err != nil {
		return fmt.Errorf("convert public key into hash: %w", err)
	}

	// Write signature file into repo folder.
	sigFileName := fmt.Sprintf("%s.yaml.sig.%s", fileName, pubKeyHash)

	// Convert file object to bytes
	fileBytes, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("failed to marshal file to YAML: %w", err)
	}

	// Create detached signature from the bytes.
	signatureBytes, err := createDetachedSignature(fileBytes, pgpEntity)
	if err != nil {
		return fmt.Errorf("failed to create detached signature for MSR: %w", err)
	}
	if err := p.createFileAndAddToWorktree(worktree, repoSignaturesFolderPath, sigFileName, signatureBytes); err != nil {
		return fmt.Errorf("create %s file and add to worktree: %w", sigFileName, err)
	}

	return nil
}

func (p *gitProvider) convertPublicKeyToHash(
	pgpEntity *openpgp.Entity,
) (string, error) {
	// Encode the public key to raw bytes
	pubKeyBuf := new(bytes.Buffer)
	err := pgpEntity.PrimaryKey.Serialize(pubKeyBuf)
	if err != nil {
		return "", fmt.Errorf("failed to serialize public key: %w", err)
	}

	// Hash the public key bytes
	hasher := sha256.New()
	hasher.Write(pubKeyBuf.Bytes())

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// InitializeGovernance creates an entry in the .qubmangi/index.yaml file with governanceIndexAlias as key and folder as value
// Second parameter returns, whether index is new (true), of existed before (false). If it's false - first parameter is empty string, but err == nil.
// If err != nil, default second parameter value is false.
func (p *gitProvider) InitializeGovernance(
	ctx context.Context,
	operationalFileLocation, governanceIndexAlias string,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (string, bool, error) {
	worktree, rollback, gpgEntity, err := p.syncAndLock(ctx)
	if err != nil {
		return "", false, fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	fullOperationalFilePath := filepath.Join(p.localPath, operationalFileLocation)

	// Check correctness of operational file name (a yaml file with non-empty name)
	fileName := filepath.Base(fullOperationalFilePath)
	if !strings.HasSuffix(fileName, ".yaml") || fileName == ".yaml" {
		return "", false, fmt.Errorf("incorrect operational .yaml file name %s", fileName)
	}

	var indexFile governancev1alpha1.QubmangoIndex
	// Check if index file exists
	if _, err := os.Stat(fullOperationalFilePath); os.IsNotExist(err) {
		// File doesn't exist. We will create a new indexFile in memory
		indexFile = governancev1alpha1.QubmangoIndex{
			TypeMeta: metav1.TypeMeta{
				APIVersion: governancev1alpha1.GroupVersion.String(),
				Kind:       "QubmangoIndex",
			},
		}

		// Create a bare file for index
		os.MkdirAll(filepath.Dir(fullOperationalFilePath), 0644)
		os.WriteFile(fullOperationalFilePath, nil, 0644)
	} else if err != nil {
		return "", false, fmt.Errorf("stat qubmango index file: %w", err)
	} else {
		// File exists - read and unmarshal
		fileBytes, err := os.ReadFile(fullOperationalFilePath)
		if err != nil {
			return "", false, fmt.Errorf("read qubmango index file: %w", err)
		}
		if err := yaml.Unmarshal(fileBytes, &indexFile); err != nil {
			return "", false, fmt.Errorf("unmarshal qubmango index file: %w", err)
		}
	}

	// Check, if index with such alias already exist. Alias should be unique
	idx := slices.IndexFunc(indexFile.Spec.Policies, func(policy governancev1alpha1.QubmangoPolicy) bool {
		return policy.Alias == governanceIndexAlias
	})
	if idx != -1 {
		// Index already exists - do nothing and return false (no new index was created).
		return "", false, nil
	}

	// Append new policy and add to staging tree
	policy := governancev1alpha1.QubmangoPolicy{
		Alias:          governanceIndexAlias,
		GovernancePath: mrt.Spec.Locations.GovernancePath,
		MSR:            mrt.Spec.MSR,
		MCA:            mrt.Spec.MCA,
	}
	indexFile.Spec.Policies = append(indexFile.Spec.Policies, policy)

	// Write back indexFile to filesystem
	updatedFileBytes, err := yaml.Marshal(indexFile)
	if err != nil {
		return "", false, fmt.Errorf("marshal updated index file: %w", err)
	}

	if err := os.WriteFile(fullOperationalFilePath, updatedFileBytes, 0644); err != nil {
		return "", false, fmt.Errorf("write updated index file: %w", err)
	}

	// Add the modified index file to the staging tree
	if _, err = worktree.Add(operationalFileLocation); err != nil {
		return "", false, fmt.Errorf("failed to git add qubmango index file to the staging area: %w", err)
	}

	// Created signed commit and push it to the remote repo
	commitMsg := fmt.Sprintf("create entry in .governance/index.yaml file for %s", governanceIndexAlias)
	commitHash, err := p.commitAndPush(ctx, worktree, commitMsg, gpgEntity)
	if err != nil {
		rollback()
		return "", false, fmt.Errorf("commit and push: %w", err)
	}

	return commitHash, true, nil
}

func (p *gitProvider) pushWorkflow(
	ctx context.Context,
	files []fileToPush,
	commitMsg string,
) (string, error) {
	worktree, rollback, gpgEntity, err := p.syncAndLock(ctx)
	if err != nil {
		return "", fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	// Sign and add the governance files to the governance folder in the worktree
	if err := p.stageGovernanceFiles(worktree, gpgEntity, files); err != nil {
		rollback()
		return "", fmt.Errorf("stage governance files: %w", err)
	}

	// Created signed commit and push it to the remote repo
	commitHash, err := p.commitAndPush(ctx, worktree, commitMsg, gpgEntity)
	if err != nil {
		rollback()
		return "", fmt.Errorf("commit and push: %w", err)
	}

	return commitHash, nil
}

func (p *gitProvider) syncAndLock(
	ctx context.Context,
) (*git.Worktree, func(), *openpgp.Entity, error) {
	if err := p.Sync(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to sync repository before push: %w", err)
	}

	p.mu.Lock()

	worktree, err := p.repo.Worktree()
	if err != nil {
		p.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("could not get repository worktree: %w", err)
	}
	gpgEntity, err := p.getGpgEntity(ctx)
	if err != nil {
		p.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("failed to load GPG signing key: %w", err)
	}

	headRef, err := p.repo.Head()
	if err != nil {
		p.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("could not get current HEAD before commit: %w", err)
	}
	originalCommitHash := headRef.Hash()
	rollback := func() {
		worktree.Reset(&git.ResetOptions{
			Commit: originalCommitHash,
			Mode:   git.HardReset,
		})
	}

	return worktree, rollback, gpgEntity, nil
}

func (p *gitProvider) stageGovernanceFiles(
	worktree *git.Worktree,
	pgpEntity *openpgp.Entity,
	files []fileToPush,
) error {
	for _, f := range files {
		if err := p.createYAMLFileWithSignatureAndAttach(worktree, pgpEntity, f.Object, f.Name, f.Folder, f.Version); err != nil {
			return fmt.Errorf("add file %s to worktree: %w", f.Name, err)
		}
	}
	return nil
}

func (p *gitProvider) createYAMLFileWithSignatureAndAttach(
	worktree *git.Worktree,
	pgpEntity *openpgp.Entity,
	file any,
	fileName, inRepoFolderPath string,
	version int,
) error {
	// Create folder for new file and signature
	repoRequestFolderPath := filepath.Join(inRepoFolderPath, fmt.Sprintf("v_%d", version))
	os.MkdirAll(filepath.Join(p.localPath, repoRequestFolderPath), 0644)

	// Write file and sig files into repo folder
	fileNameExtended := fmt.Sprintf("%s.yaml", fileName)
	sigFileName := fmt.Sprintf("%s.yaml.sig", fileName)

	// Convert file object to bytes
	fileBytes, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("failed to marshal file to YAML: %w", err)
	}
	// Create detached signature from the bytes
	signatureBytes, err := createDetachedSignature(fileBytes, pgpEntity)
	if err != nil {
		return fmt.Errorf("failed to create detached signature for MSR: %w", err)
	}

	if err := p.createFileAndAddToWorktree(worktree, repoRequestFolderPath, fileNameExtended, fileBytes); err != nil {
		return fmt.Errorf("create %s file and add to worktree: %w", fileNameExtended, err)
	}
	if err := p.createFileAndAddToWorktree(worktree, repoRequestFolderPath, sigFileName, signatureBytes); err != nil {
		return fmt.Errorf("create %s file and add to worktree: %w", sigFileName, err)
	}

	return nil
}

func (p *gitProvider) createFileAndAddToWorktree(
	worktree *git.Worktree,
	repoFolderPath, fileName string, file []byte,
) error {
	filePath := filepath.Join(p.localPath, repoFolderPath, fileName)
	repoPath := filepath.Join(repoFolderPath, fileName)

	err := os.WriteFile(filePath, file, 0644)
	if err != nil {
		return fmt.Errorf("failed to write %s file: %w", fileName, err)
	}

	if _, err = worktree.Add(repoPath); err != nil {
		return fmt.Errorf("failed to git add file %s to the staging area: %w", fileName, err)
	}

	return nil
}

func (p *gitProvider) commitAndPush(
	ctx context.Context,
	worktree *git.Worktree,
	commitMsg string,
	pgpEntity *openpgp.Entity,
) (string, error) {
	commitOpts := &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Qubmango Governance Operator",
			Email: "noreply@qubmango.com",
			When:  time.Now(),
		},
		SignKey: pgpEntity,
	}
	commitHash, err := worktree.Commit(commitMsg, commitOpts)
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		Auth:       p.auth,
	}
	err = p.repo.PushContext(ctx, pushOpts)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return "", fmt.Errorf("failed to push commit: %w", err)
	}
	return commitHash.String(), nil
}

func (p *gitProvider) getGpgEntity(
	ctx context.Context,
) (*openpgp.Entity, error) {
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
func createDetachedSignature(
	fileContent []byte,
	signKey *openpgp.Entity,
) ([]byte, error) {
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
