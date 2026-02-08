package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/go-logr/logr"
	"gopkg.in/yaml.v2"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
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

// BaseGitProvider contains common git operations shared across all provider implementations.
type BaseGitProvider struct {
	remoteURL  string
	localPath  string
	repo       *git.Repository
	auth       transport.AuthMethod
	logger     logr.Logger
	mu         sync.Mutex
	pgpSecrets PgpSecrets
}

// NewBaseGitProvider creates a new base git provider instance.
func NewBaseGitProvider(
	remoteURL, localPath string,
	auth transport.AuthMethod,
	logger logr.Logger,
	pgpSecrets PgpSecrets,
) *BaseGitProvider {
	return &BaseGitProvider{
		remoteURL:  remoteURL,
		localPath:  localPath,
		auth:       auth,
		logger:     logger,
		pgpSecrets: pgpSecrets,
	}
}

// UpdateSecrets updates the provider's SSH authentication and PGP secrets.
func (p *BaseGitProvider) UpdateSecrets(
	auth transport.AuthMethod,
	pgpSecrets PgpSecrets,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.auth = auth
	p.pgpSecrets = pgpSecrets

	return nil
}

// Sync ensures the local repository is cloned and up-to-date.
func (p *BaseGitProvider) Sync(
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

func (p *BaseGitProvider) HasRevision(
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

func (p *BaseGitProvider) IsNotAfter(
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

// GetRemoteHeadCommit returns the SHA of the HEAD branch. Uses ls-remote.
func (p *BaseGitProvider) GetRemoteHeadCommit(
	ctx context.Context,
) (string, error) {
	// Create the remote
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{p.remoteURL},
	})

	// List references
	refs, err := rem.List(&git.ListOptions{
		Auth: p.auth,
	})
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}

	refMap := make(map[string]*plumbing.Reference)
	for _, ref := range refs {
		refMap[ref.Name().String()] = ref
	}

	// Find HEAD
	headRef, found := refMap["HEAD"]
	if !found {
		return "", fmt.Errorf("HEAD reference not found")
	}

	// Resolve Symbolic Reference
	if headRef.Type() == plumbing.SymbolicReference {
		targetName := headRef.Target().String()
		targetRef, found := refMap[targetName]
		if !found {
			return "", fmt.Errorf("HEAD targets %s, but it was not found in refs", targetName)
		}
		return targetRef.Hash().String(), nil
	}

	return headRef.Hash().String(), nil
}

// GetLocalHeadCommit returns the SHA of the local HEAD branch.
func (p *BaseGitProvider) GetLocalHeadCommit(ctx context.Context) (string, error) {
	if p.repo == nil {
		return "", fmt.Errorf("repository has not been initialized; call Sync() first")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	// Get the reference for HEAD.
	headRef, err := p.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD reference from local repository: %w", err)
	}

	return headRef.Hash().String(), nil
}

// GetLatestRevision takes head of the default branch and returns it's commit hash
func (p *BaseGitProvider) GetLatestRevision(
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

func (p *BaseGitProvider) GetChangedFiles(
	ctx context.Context,
	fromCommit, toCommit string,
	fromFolder string,
) ([]governancev1alpha1.FileChange, map[string]string, error) {
	if err := p.Sync(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to sync repository before getting changed files: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Get toTree for toCommit
	toTree, err := p.getTreeForCommit(ctx, toCommit)
	if err != nil {
		return nil, nil, err
	}

	// Get fromTree for fromCommit
	var fromTree *object.Tree
	if fromCommit == "" {
		// Handle empty tree case
		fromTree = &object.Tree{}
	} else {
		fromTree, err = p.getTreeForCommit(ctx, fromCommit)
		if err != nil {
			return nil, nil, err
		}
	}

	// Get patch (file difference) between commits
	patch, err := fromTree.Patch(toTree)
	if err != nil {
		return nil, nil, fmt.Errorf("could not compute patch between commits: %w", err)
	}

	return p.patchToFileChangeList(fromTree, toTree, patch, fromFolder)
}

func (p *BaseGitProvider) getTreeForCommit(
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

func (p *BaseGitProvider) patchToFileChangeList(
	fromTree *object.Tree,
	toTree *object.Tree,
	patch *object.Patch,
	fromFolder string,
) ([]governancev1alpha1.FileChange, map[string]string, error) {
	var changes []governancev1alpha1.FileChange
	contentMap := make(map[string]string)
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
			return nil, nil, fmt.Errorf("could not get file object for path %s: %w", path, err)
		}

		if !strings.HasPrefix(path, fromFolderNormalized+"/") {
			continue
		}

		// Get sha256 and meta
		var sha256Hex string
		var meta k8sObjectMetadata

		content, err := file.Contents()
		if err != nil {
			return nil, nil, fmt.Errorf("could not read file contents for %s: %w", path, err)
		}

		if err := yaml.Unmarshal([]byte(content), &meta); err != nil {
			// Could be a non-k8s file, or malformed. Log and skip for now.
			continue
		}

		// Calculate SHA256
		hasher := sha256.New()
		hasher.Write([]byte(content))
		sha256Hex = hex.EncodeToString(hasher.Sum(nil))

		changes = append(changes, governancev1alpha1.FileChange{
			Path:      path,
			Status:    status,
			Kind:      meta.Kind,
			Name:      meta.Metadata.Name,
			Namespace: meta.Metadata.Namespace,
			SHA256:    sha256Hex,
		})
		contentMap[path] = content
	}

	return changes, contentMap, nil
}

func (p *BaseGitProvider) PushMSR(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequestManifestObject,
) (string, error) {
	files := []fileToPush{
		{Object: msr, Name: msr.ObjectMeta.Name, Folder: msr.Spec.Locations.GovernancePath, Version: msr.Spec.Version},
	}
	msg := fmt.Sprintf("New ManifestSigningRequest: create manifest signing request %s with version %d", msr.ObjectMeta.Name, msr.Spec.Version)
	return p.pushWorkflow(ctx, files, msg)
}

func (p *BaseGitProvider) PushMCA(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApprovalManifestObject,
) (string, error) {
	files := []fileToPush{
		{Object: mca, Name: mca.ObjectMeta.Name, Folder: mca.Spec.Locations.GovernancePath, Version: mca.Spec.Version},
	}
	msg := fmt.Sprintf("New ManifestChangeApproval: create manifest change approval %s with version %d", mca.ObjectMeta.Name, mca.Spec.Version)
	return p.pushWorkflow(ctx, files, msg)
}

// PushSummaryFile creates, signs and pushes summary files (apply or delete manifests) to the Git repository.
// It handles raw content (string) and creates both the manifest file and its signature.
func (p *BaseGitProvider) PushSummaryFile(
	ctx context.Context,
	content, fileName, governanceFolder string,
	version int,
) (string, error) {
	worktree, rollback, pgpEntity, err := p.syncAndLock(ctx)
	if err != nil {
		return "", fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	if err := p.createYAMLFileWithSignatureAndAttach(worktree, pgpEntity, []byte(content), fileName, governanceFolder, version); err != nil {
		return "", fmt.Errorf("add file %s to worktree: %w", fileName, err)
	}

	// Create signed commit and push it to the remote repo
	commitMsg := fmt.Sprintf("Add summary file: %s version %d", fileName, version)
	commitHash, err := p.commitAndPush(ctx, worktree, commitMsg, pgpEntity)
	if err != nil {
		rollback()
		return "", fmt.Errorf("commit and push: %w", err)
	}

	return commitHash, nil
}

func (p *BaseGitProvider) PushGovernorSignature(
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

func (p *BaseGitProvider) createFileGovernorSignatureAndAttach(
	worktree *git.Worktree,
	pgpEntity *openpgp.Entity,
	file any,
	fileName, inRepoFolderPath string,
	version int,
) error {
	// Create signatures folder.
	repoSignaturesFolderPath := filepath.Join(inRepoFolderPath, fmt.Sprintf("v_%d", version), "signatures")
	os.MkdirAll(filepath.Join(p.localPath, repoSignaturesFolderPath), 0755)

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

func (p *BaseGitProvider) convertPublicKeyToHash(
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

func (p *BaseGitProvider) FetchMSRByVersion(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (*dto.ManifestSigningRequestManifestObject, []byte, dto.SignatureData, []dto.SignatureData, error) {
	// Sync and Lock
	if err := p.Sync(ctx); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("sync repository: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	worktree, err := p.repo.Worktree()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get repository worktree: %w", err)
	}

	// Construct the path for the specific version
	// Path format: GovernancePath/v_{Version}/
	msrFolderPath := filepath.Join(msr.Spec.Locations.GovernancePath, fmt.Sprintf("v_%d", msr.Spec.Version))

	// Extract MSR Content
	msrFilePath := filepath.Join(msrFolderPath, fmt.Sprintf("%s.yaml", msr.ObjectMeta.Name))
	msrFile, err := worktree.Filesystem.Open(msrFilePath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open msr file %s: %w", msrFilePath, err)
	}
	defer msrFile.Close()

	msrContent, err := io.ReadAll(msrFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read msr file content: %w", err)
	}

	var fetchedMSR dto.ManifestSigningRequestManifestObject
	if err := yaml.Unmarshal(msrContent, &fetchedMSR); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("unmarshal msr file content: %w", err)
	}

	// Read msr signature (msr.yaml.sig)
	var msrSignature dto.SignatureData
	appSigPath := filepath.Join(msrFolderPath, fmt.Sprintf("%s.yaml.sig", msr.ObjectMeta.Name))
	msrSigFile, err := worktree.Filesystem.Open(appSigPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open msr signature file: %w", err)
	}
	defer msrSigFile.Close()

	sigBytes, err := io.ReadAll(msrSigFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read msr signature: %w", err)
	}
	msrSignature = dto.SignatureData(sigBytes)

	// Fetch governor signatures
	var goverSignatures []dto.SignatureData
	signaturesFolderPath := filepath.Join(msrFolderPath, "signatures")

	sigFileInfos, err := worktree.Filesystem.ReadDir(signaturesFolderPath)
	if err != nil && os.IsNotExist(err) {
		// It's okay if the signatures folder doesn't exist yet (e.g., initial creation)
		goverSignatures = []dto.SignatureData{}
	} else if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read signatures folder: %w", err)
	} else {
		// Regex to match: msr.yaml.sig.{suffix}
		govSigMatcher := regexp.MustCompile(fmt.Sprintf(`^%s\.yaml\.sig\..+$`, msr.ObjectMeta.Name))

		for _, sigFile := range sigFileInfos {
			if sigFile.IsDir() {
				continue
			}

			if govSigMatcher.MatchString(sigFile.Name()) {
				fPath := filepath.Join(signaturesFolderPath, sigFile.Name())
				f, err := worktree.Filesystem.Open(fPath)
				if err != nil {
					continue // Skip unreadable files
				}

				content, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}

				goverSignatures = append(goverSignatures, dto.SignatureData(content))
			}
		}
	}

	return &fetchedMSR, msrContent, msrSignature, goverSignatures, nil
}

func (p *BaseGitProvider) pushWorkflow(
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

func (p *BaseGitProvider) syncAndLock(
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
	gpgEntity, err := p.getPGPEntity(ctx)
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

func (p *BaseGitProvider) stageGovernanceFiles(
	worktree *git.Worktree,
	pgpEntity *openpgp.Entity,
	files []fileToPush,
) error {
	for _, f := range files {
		fileBytes, err := yaml.Marshal(f.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal file to YAML: %w", err)
		}

		if err := p.createYAMLFileWithSignatureAndAttach(worktree, pgpEntity, fileBytes, f.Name, f.Folder, f.Version); err != nil {
			return fmt.Errorf("add file %s to worktree: %w", f.Name, err)
		}
	}
	return nil
}

func (p *BaseGitProvider) createYAMLFileWithSignatureAndAttach(
	worktree *git.Worktree,
	pgpEntity *openpgp.Entity,
	fileBytes []byte,
	fileName, governanceFolder string,
	version int,
) error {
	// Create folder for new file and signature
	repoRequestFolderPath := filepath.Join(governanceFolder, fmt.Sprintf("v_%d", version))
	if err := os.MkdirAll(filepath.Join(p.localPath, repoRequestFolderPath), 0755); err != nil {
		return fmt.Errorf("create governance folder: %w", err)
	}

	// Write file and sig files into repo folder
	fileNameExtended := fmt.Sprintf("%s.yaml", fileName)
	sigFileName := fmt.Sprintf("%s.yaml.sig", fileName)

	// Convert file object to bytes
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

func (p *BaseGitProvider) createFileAndAddToWorktree(
	worktree *git.Worktree,
	repoFolderPath, fileName string, file []byte,
) error {
	filePath := filepath.Join(p.localPath, repoFolderPath, fileName)
	repoPath := filepath.Join(repoFolderPath, fileName)

	err := os.WriteFile(filePath, file, 0755)
	if err != nil {
		return fmt.Errorf("failed to write %s file: %w", fileName, err)
	}

	if _, err = worktree.Add(repoPath); err != nil {
		return fmt.Errorf("failed to git add file %s to the staging area: %w", fileName, err)
	}

	return nil
}

func (p *BaseGitProvider) commitAndPush(
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

func (p *BaseGitProvider) getPGPEntity(
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

	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		return nil, fmt.Errorf("PGP private key remains encrypted after decrypt attempt")
	}

	if len(entity.Subkeys) > 0 {
		if p.pgpSecrets.Passphrase == "" {
			for _, subkey := range entity.Subkeys {
				if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
					return nil, fmt.Errorf("PGP subkey is encrypted, but no passphrase was provided")
				}
			}
		} else {
			passphrase := []byte(p.pgpSecrets.Passphrase)
			for _, subkey := range entity.Subkeys {
				if subkey.PrivateKey == nil || !subkey.PrivateKey.Encrypted {
					continue
				}
				if err := subkey.PrivateKey.Decrypt(passphrase); err != nil || subkey.PrivateKey.Encrypted {
					return nil, fmt.Errorf("failed to decrypt PGP subkey: %w", err)
				}
			}
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
		armorWriter.Close()
		return nil, fmt.Errorf("failed to create detached signature: %w", err)
	}

	if err := armorWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize signature: %w", err)
	}

	return sigBuf.Bytes(), nil
}

func (p *BaseGitProvider) DeleteFolder(ctx context.Context, folderPath string) error {
	worktree, rollback, pgpEntity, err := p.syncAndLock(ctx)
	if err != nil {
		return fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	// Build path to the folder
	localFolderPath := filepath.Join(p.localPath, folderPath)

	// Check if it exists
	if _, err := os.Stat(localFolderPath); err != nil && errors.Is(err, os.ErrNotExist) {
		// Nothing to delete
		return nil
	}

	// Stage the deletion
	if err := worktree.RemoveGlob(filepath.Join(folderPath, "**")); err != nil {
		rollback()
		return fmt.Errorf("failed to stage folder deletion in git: %w", err)
	}

	// Remove the folder from the local worktree
	if err := os.RemoveAll(localFolderPath); err != nil {
		rollback()
		return fmt.Errorf("failed to remove folder from local path: %w", err)
	}
	// Create signed commit and push it to the remote repo
	commitMsg := fmt.Sprintf("Delete governance folder: %s", folderPath)
	_, err = p.commitAndPush(ctx, worktree, commitMsg, pgpEntity)
	if err != nil {
		rollback()
		return fmt.Errorf("commit and push: %w", err)
	}

	return nil
}
