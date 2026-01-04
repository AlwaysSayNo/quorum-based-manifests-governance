package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"gopkg.in/yaml.v2"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	crypto "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/crypto"
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
func (f *GitProviderFactory) New(
	ctx context.Context,
	remoteURL, localPath string,
	auth transport.AuthMethod,
	pgpSecrets crypto.Secrets,
) (manager.GitRepositoryProvider, error) {
	p := &gitProvider{
		remoteURL:  remoteURL,
		localPath:  localPath,
		auth:       auth,
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
	// A mutex to protect repo from concurrent git operations
	mu         sync.Mutex
	pgpSecrets crypto.Secrets
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

func (p *gitProvider) GetChangedFilesRaw(
	ctx context.Context,
	fromCommit, toCommit string,
	fromFolder string,
) (map[string]manager.FileBytesWithStatus, error) {
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
	fromTree, err := p.getTreeForCommit(ctx, fromCommit)
	if err != nil {
		return nil, err
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
	if hash == "" {
		// Handle empty tree case
		return &object.Tree{}, nil
	}

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
	fromTree, toTree *object.Tree,
	patch *object.Patch,
	fromFolder string,
) (map[string]manager.FileBytesWithStatus, error) {
	result := make(map[string]manager.FileBytesWithStatus)
	fromFolderNormalized := filepath.Clean(fromFolder)

	for _, filePatch := range patch.FilePatches() {
		path, status, file, err := p.getFileMetaInfoForPath(fromTree, toTree, filePatch)
		if err != nil {
			return nil, fmt.Errorf("could not get file object for path %s: %w", path, err)
		}

		if !strings.HasPrefix(path, fromFolderNormalized+"/") {
			continue
		}

		content, err := file.Contents()
		if err != nil {
			return nil, fmt.Errorf("could not read file contents for %s: %w", path, err)
		}
		result[path] = manager.FileBytesWithStatus{
			Content: []byte(content),
			Status:  status,
			Path:    path,
		}
	}

	return result, nil
}

func (p *gitProvider) getFileMetaInfoForPath(
	fromTree, toTree *object.Tree,
	filePatch diff.FilePatch,
) (string, manager.FileChangeStatus, *object.File, error) {
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
		err = fmt.Errorf("unsupported patch state: from and to are both nil")
	}

	return path, status, file, err
}

func (p *gitProvider) PushGovernorSignature(
	ctx context.Context,
	msr *manager.ManifestSigningRequestManifestObject,
	user config.UserInfo,
) (string, error) {
	worktree, rollback, pgpEntity, err := p.syncAndLock3(ctx)
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
	commitMsg := fmt.Sprintf("%s signs ManifestSigningRequest %s with version %d", user.Name, msr.ObjectMeta.Name, msr.Spec.Version)
	commitHash, err := p.commitAndPush(ctx, worktree, commitMsg, pgpEntity, user)
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
	pubKeyHash, err := crypto.ConvertPublicKeyToHash(pgpEntity)
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
	signatureBytes, err := crypto.CreateDetachedSignatureByEntity(fileBytes, pgpEntity)
	fmt.Println(string(signatureBytes))
	if err != nil {
		return fmt.Errorf("failed to create detached signature for MSR: %w", err)
	}
	if err := p.createFileAndAddToWorktree(worktree, repoSignaturesFolderPath, sigFileName, signatureBytes); err != nil {
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
	user config.UserInfo,
) (string, error) {
	if user.Email == "" || user.Name == "" {
		return "", fmt.Errorf("user information missing (email: %s, name: %s)", user.Email, user.Name)
	}
	commitOpts := &git.CommitOptions{
		Author: &object.Signature{
			Name:  user.Name,
			Email: user.Email,
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

func (p *gitProvider) GetQubmangoIndex(ctx context.Context,
) (*manager.QubmangoIndex, error) {
	return p.getQubmangoIndexWithPath(ctx, manager.QubmangoIndexFilePath)
}

func (p *gitProvider) getQubmangoIndexWithPath(
	ctx context.Context,
	qubmangoFileRepositoryPath string,
) (*manager.QubmangoIndex, error) {
	_, _, err := p.syncAndLock2(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	fullFilePath := filepath.Join(p.localPath, qubmangoFileRepositoryPath)

	// Check correctness of operational file name (a yaml file with non-empty name)
	fileName := filepath.Base(fullFilePath)
	if !strings.HasSuffix(fileName, ".yaml") || fileName == ".yaml" {
		return nil, fmt.Errorf("incorrect operational .yaml file name %s", fileName)
	}

	if _, err := os.Stat(fullFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("index file doesn't exist in %s", fullFilePath)
	}

	// File exists - read and unmarshal
	fileBytes, err := os.ReadFile(fullFilePath)
	if err != nil {
		return nil, fmt.Errorf("read qubmango index file: %w", err)
	}

	var qubmangoIndex manager.QubmangoIndex
	if err := yaml.Unmarshal(fileBytes, &qubmangoIndex); err != nil {
		return nil, fmt.Errorf("unmarshal qubmango index file: %w", err)
	}

	return &qubmangoIndex, nil
}

// GetLatestMSR finds the most recent MSR in the repository and its associated signatures.
// It returns the parsed MSR, its file content, qubmango signature, a list of governor signatures, and an error if any.
func (p *gitProvider) GetLatestMSR(
	ctx context.Context,
	policy *manager.QubmangoPolicy,
) (*manager.ManifestSigningRequestManifestObject, []byte, manager.SignatureData, []manager.SignatureData, error) {
	// Sync and Lock
	worktree, _, err := p.syncAndLock2(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("sync and lock: %w", err)
	}
	defer p.mu.Unlock()

	// Get the latest MSR folder path
	activeMSRFolderPath, err := p.getLatestMSRFolder(worktree, policy)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get the latest MSR folder path: %w", err)
	}

	// Extract MSR, MSR content and signature
	msr, msrContent, msrSignature, err := p.getMSRAndSignature(activeMSRFolderPath, worktree, policy)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("extract MSR, MSR content and signature: %w", err)
	}

	// Fetch governor signatures for current msr
	goverSignatures, err := p.readGovernorSignatures(activeMSRFolderPath, worktree, policy)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("fetch governor signatures: %w", err)
	}

	return msr, msrContent, msrSignature, goverSignatures, nil
}

func (p *gitProvider) getLatestMSRFolder(
	worktree *git.Worktree,
	policy *manager.QubmangoPolicy,
) (string, error) {
	// Scan for the latest version folder (v_N)
	fileInfos, err := worktree.Filesystem.ReadDir(policy.GovernancePath)
	if err != nil {
		// If the folder doesn't exist yet, it's not an error, just no MSR found
		return "", fmt.Errorf("read governance folder %s: %w", policy.GovernancePath, err)
	}

	msrFolderMatcher := regexp.MustCompile(`^v_(\d+)$`)
	latestMSRFolderPath := ""
	msrVersion := -1

	for _, f := range fileInfos {
		if !f.IsDir() {
			continue
		}

		// Check if folder matches v_{number}
		matches := msrFolderMatcher.FindStringSubmatch(f.Name())
		if len(matches) == 2 {
			// Error ignored because regex guarantees digits
			n, _ := strconv.Atoi(matches[1])
			if n > msrVersion {
				msrVersion = n
				latestMSRFolderPath = filepath.Join(policy.GovernancePath, f.Name())
			}
		}
	}

	if msrVersion == -1 {
		return "", fmt.Errorf("no MSR version found in %s", policy.GovernancePath)
	}

	return latestMSRFolderPath, nil
}

func (p *gitProvider) getMSRAndSignature(
	activeMSRFolderPath string,
	worktree *git.Worktree,
	policy *manager.QubmangoPolicy,
) (*manager.ManifestSigningRequestManifestObject, []byte, manager.SignatureData, error) {
	// Extract MSR Content
	msrFilePath := filepath.Join(activeMSRFolderPath, fmt.Sprintf("%s.yaml", policy.MSR.Name))
	msrFile, err := worktree.Filesystem.Open(msrFilePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open msr file %s: %w", msrFilePath, err)
	}
	defer msrFile.Close()

	msrContent, err := io.ReadAll(msrFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read msr file content: %w", err)
	}

	var msr manager.ManifestSigningRequestManifestObject
	if err := yaml.Unmarshal(msrContent, &msr); err != nil {
		return nil, nil, nil, fmt.Errorf("unmarshal msr file content: %w", err)
	}

	// Read msr governance signature (msr.yaml.sig)
	var msrSignature manager.SignatureData
	appSigPath := filepath.Join(activeMSRFolderPath, fmt.Sprintf("%s.yaml.sig", policy.MSR.Name))

	msrSigFile, err := worktree.Filesystem.Open(appSigPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read msr governance signature: %w", err)
	}
	defer msrSigFile.Close()

	// Only read if file exists
	sigBytes, err := io.ReadAll(msrSigFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read app signature: %w", err)
	}
	msrSignature = manager.SignatureData(sigBytes)

	return &msr, msrContent, msrSignature, nil
}

func (p *gitProvider) readGovernorSignatures(
	activeMSRFolderPath string,
	worktree *git.Worktree,
	policy *manager.QubmangoPolicy,
) ([]manager.SignatureData, error) {
	// Read governor signatures (signatures/msr.yaml.sig.*)
	var goverSignatures []manager.SignatureData
	signaturesFolderPath := filepath.Join(activeMSRFolderPath, "signatures")

	sigFileInfos, err := worktree.Filesystem.ReadDir(signaturesFolderPath)
	if err != nil && os.IsNotExist(err) {
		// It's okay if the signatures folder doesn't exist yet (e.g., initial creation)
		return goverSignatures, nil
	} else if err != nil {
		return nil, fmt.Errorf("read signatures folder: %w", err)
	}

	// Regex to match: msr.yaml.sig.{suffix}
	govSigMatcher := regexp.MustCompile(fmt.Sprintf(`^%s\.yaml\.sig\..+$`, policy.MSR.Name))

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

			goverSignatures = append(goverSignatures, manager.SignatureData(content))
		}
	}

	return goverSignatures, nil
}

func (p *gitProvider) syncAndLock3(
	ctx context.Context,
) (*git.Worktree, func(), *openpgp.Entity, error) {
	worktree, rollback, err := p.syncAndLock2(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	pgpEntity, err := crypto.GetPGPEntity(ctx, &p.pgpSecrets)
	if err != nil {
		p.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("failed to load PGP signing key: %w", err)
	}

	return worktree, rollback, pgpEntity, nil
}

func (p *gitProvider) syncAndLock2(
	ctx context.Context,
) (*git.Worktree, func(), error) {
	if err := p.Sync(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to sync repository before push: %w", err)
	}

	p.mu.Lock()

	worktree, err := p.repo.Worktree()
	if err != nil {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("could not get repository worktree: %w", err)
	}

	headRef, err := p.repo.Head()
	if err != nil {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("could not get current HEAD before commit: %w", err)
	}
	originalCommitHash := headRef.Hash()
	rollback := func() {
		worktree.Reset(&git.ResetOptions{
			Commit: originalCommitHash,
			Mode:   git.HardReset,
		})
	}

	return worktree, rollback, nil
}

// GetFileDiffPatchParts returns a map<path, patch> for a subset of files in MSR between two commits.
// Each diff.Patch value in returned map contains only single file patch.
func (p *gitProvider) GetFileDiffPatchParts(
	ctx context.Context,
	msr *manager.ManifestSigningRequestManifestObject,
	fromCommit, toCommit string,
) (map[string]diff.Patch, error) {
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
	fromTree, err := p.getTreeForCommit(ctx, fromCommit)
	if err != nil {
		return nil, err
	}

	// Get patch (file difference) between commits
	patch, err := fromTree.Patch(toTree)
	if err != nil {
		return nil, fmt.Errorf("could not compute patch between commits: %w", err)
	}

	// Create MSR map<path, status>
	msrFCMap := make(map[string]manager.FileChangeStatus)
	for _, fc := range msr.Spec.Changes {
		msrFCMap[fc.Path] = fc.Status
	}

	// Build result map
	selectedFilePatches := make(map[string]diff.Patch)
	for _, filePatch := range patch.FilePatches() {
		path, status, _, err := p.getFileMetaInfoForPath(fromTree, toTree, filePatch)
		if err != nil {
			return nil, fmt.Errorf("could not get file object for path %s: %w", path, err)
		}

		if msrStatus, exists := msrFCMap[path]; exists && msrStatus == status {
			selectedFilePatches[path] = manager.NewMyFilePatch([]diff.FilePatch{filePatch}, patch.Message())
		}
	}

	// Check, that result and MSR changes have the same length
	if len(selectedFilePatches) != len(msr.Spec.Changes) {
		return nil, fmt.Errorf("end file slice is %d elements long, when MSR has %d", len(selectedFilePatches), len(msr.Spec.Changes))
	}
	return selectedFilePatches, nil
}
