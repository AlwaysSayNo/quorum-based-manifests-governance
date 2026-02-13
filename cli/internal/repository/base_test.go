package repository

import (
	"bytes"
	"context"
	"crypto"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	internalconfig "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	cryptolib "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/crypto"
	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"
)

// setupTestRepo creates a temporary local git setup for testing.
// It returns the path to the `remote` repo and a `workspace` clone.
// The `workspace` is used to simulate a developer making new commits.
func setupTestRepo() (remotePath, workspacePath string) {
	baseDir := GinkgoT().TempDir()

	// Define paths for our THREE repositories
	initRepoPath := filepath.Join(baseDir, "initial-repo")
	remotePath = filepath.Join(baseDir, "remote.git")
	workspacePath = filepath.Join(baseDir, "workspace")

	// Create init non-bare repo (false - non-bare)
	initRepo, err := git.PlainInit(initRepoPath, false)
	Expect(err).NotTo(HaveOccurred())

	// Create initial commit to the repo (true - bare)
	makeCommit(initRepo, initRepoPath, "README.md", "initial commit", "Initial commit")

	// Create remote bare repo (clone from init)
	_, err = git.PlainClone(remotePath, true, &git.CloneOptions{
		URL: initRepoPath,
	})
	Expect(err).NotTo(HaveOccurred())

	// Create workspace repo by cloning non-empty bare remote (false - non-bare)
	_, err = git.PlainClone(workspacePath, false, &git.CloneOptions{
		URL: remotePath,
	})
	Expect(err).NotTo(HaveOccurred())

	return remotePath, workspacePath
}

// makeCommit is a shortcut function, that creates file by the given fileName and commits
func makeCommit(repo *git.Repository, repoPath, fileName, content, msg string) string {
	// Create folders, if any exist in the fileName
	parts := strings.Split(fileName, "/")
	path := repoPath
	for i := 0; i < len(parts)-1; i++ {
		path = filepath.Join(path, parts[i])
	}
	err := os.MkdirAll(path, 0644)
	Expect(err).NotTo(HaveOccurred())

	worktree, err := repo.Worktree()
	Expect(err).NotTo(HaveOccurred())

	err = os.WriteFile(filepath.Join(repoPath, fileName), []byte(content), 0644)
	Expect(err).NotTo(HaveOccurred())
	_, err = worktree.Add(fileName)
	Expect(err).NotTo(HaveOccurred())
	commitHash, err := worktree.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.com"},
	})
	Expect(err).NotTo(HaveOccurred())

	return commitHash.String()
}

func makeCommitAndPush(repo *git.Repository, repoPath, fileName, content, msg string) string {
	commitHashString := makeCommit(repo, repoPath, fileName, content, msg)
	Expect(repo.Push(&git.PushOptions{})).To(Succeed())

	return commitHashString
}

var _ = Describe("gitProvider Sync Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *BaseGitProvider
		localPath     string
		ctx           context.Context
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when the repository is already held in memory", func() {
		It("should successfully pull new changes", func() {
			// SETUP
			// First sync. Should setup the repo in the provider
			Expect(provider.Sync(ctx)).To(Succeed())

			// Make changes in the workspaceRepo and push them
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			makeCommitAndPush(workspaceRepo, workspacePath, "new-file.txt", "new data", "Second commit")

			// ACT
			// Second sync. It should only pull the latest changes
			err = provider.Sync(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			_, err = os.Stat(filepath.Join(localPath, "new-file.txt")) // Check that the new file exists in the provider local clone
			Expect(os.IsNotExist(err)).To(BeFalse())
		})
	})

	Context("when a local copy already exists on disk, but not in memory", func() {
		It("should open the existing repository and pull successfully", func() {
			// SETUP
			// Manually create a clone at the localPath. But provider is still uninitialized
			_, err := git.PlainClone(localPath, false, &git.CloneOptions{
				URL: remotePath,
			})
			Expect(err).NotTo(HaveOccurred())

			// ACT
			// Sync should find the repo on disk, open and pull it.
			err = provider.Sync(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(provider.repo).NotTo(BeNil())
		})
	})

	Context("when no local copy exists", func() {
		It("should successfully clone remote repository", func() {
			// SETUP
			// provider is nil and localPath directory is empty

			// ACT
			err := provider.Sync(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(provider.repo).NotTo(BeNil())
			_, err = os.Stat(filepath.Join(localPath, "README.md"))
			Expect(os.IsNotExist(err)).To(BeFalse())
		})

		It("should fail if the remote URL is invalid", func() {
			// SETUP
			badProvider := &BaseGitProvider{
				remoteURL: "/no/such/repo.git",
				localPath: localPath,
				logger:    GinkgoLogr,
			}

			// ACT
			err := badProvider.Sync(ctx)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(badProvider.repo).To(BeNil())
		})
	})
})

var _ = Describe("gitProvider GetLatestRevision Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *BaseGitProvider
		localPath     string
		ctx           context.Context
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when the repository is synced and healthy", func() {
		It("should return the correct hash of the latest commit", func() {
			// SETUP
			// Get expected value
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			expectedHash := headRef.Hash().String()

			// ACT
			actualHash, err := provider.GetLatestRevision(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(actualHash).To(Equal(expectedHash))
		})
	})

	Context("when the initial Sync fails", func() {
		It("should return an error", func() {
			// SETUP
			badProvider := &BaseGitProvider{
				remoteURL: "/invalid/path/to/remote.git",
				localPath: localPath,
				logger:    GinkgoLogr,
			}

			// ACT
			// inner sync call should fail
			latestHash, err := badProvider.GetLatestRevision(ctx)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to sync repository"))
			Expect(latestHash).To(BeEmpty())
		})
	})
})

var _ = Describe("gitProvider HasRevision Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *BaseGitProvider
		localPath     string
		ctx           context.Context
		initialCommit plumbing.Hash
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}

		// Get the hash of the first commit in the remote repo
		remoteRepo, err := git.PlainOpen(remotePath)
		Expect(err).NotTo(HaveOccurred())
		headRef, err := remoteRepo.Head()
		Expect(err).NotTo(HaveOccurred())
		initialCommit = headRef.Hash()
	})

	Context("when the searched commit is an ancestor of the default branch", func() {
		It("should return true", func() {
			// SETUP

			// Create new commit
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			makeCommitAndPush(workspaceRepo, workspacePath, "another-file.txt", "data", "New commit")

			// ACT
			// initialCommit from BeforeEach is an ancestor of the new commit
			found, err := provider.HasRevision(ctx, initialCommit.String())

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
		})
	})

	Context("when the searched commit is the HEAD of the default branch", func() {
		It("should return true", func() {
			// SETUP

			// ACT
			// initialCommit is the current HEAD
			found, err := provider.HasRevision(ctx, initialCommit.String())

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
		})
	})

	Context("when the searched commit does not exist in the repository", func() {
		It("should return false", func() {
			// SETUP
			fakeCommit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

			// ACT
			found, err := provider.HasRevision(ctx, fakeCommit)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())
		})
	})

	Context("when the searched commit exists but is on an unmerged feature-branch", func() {
		It("should return false", func() {
			// SETUP
			// Create new commit on a separate branch
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())

			// Create and checkout feature-branch
			branchName := plumbing.NewBranchReferenceName("feature-branch")
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: branchName,
				Create: true,
			})
			Expect(err).NotTo(HaveOccurred())

			// Create a commit on feature-branch
			err = os.WriteFile(filepath.Join(workspacePath, "feature.txt"), []byte("feature data"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("feature.txt")
			Expect(err).NotTo(HaveOccurred())
			featureCommitHash, err := worktree.Commit("Feature commit", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Push feature-branch to the remote
			Expect(workspaceRepo.Push(&git.PushOptions{
				RefSpecs: []config.RefSpec{"+refs/heads/feature-branch:refs/heads/feature-branch"},
			})).To(Succeed())

			// ACT
			// Search for the feature-branch commit hash
			found, err := provider.HasRevision(ctx, featureCommitHash.String())

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())
		})
	})
})

var _ = Describe("gitProvider GetChangedFilesRaw Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *BaseGitProvider
		localPath     string
		ctx           context.Context
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when the 'toCommit' hash is invalid", func() {
		It("should return an error", func() {
			// SETUP

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "app-manifests")

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not find commit"))
			Expect(changes).To(BeNil())
		})
	})

	Context("when the 'toCommit' is valid, but 'fromCommit' hash is invalid", func() {
		It("should return an error", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "file1.txt", "content1", "Commit 1")

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not find commit"))
			Expect(changes).To(BeNil())
		})
	})

	Context("when there are commits but no changes in the monitored folder", func() {
		It("should return an empty list of changes", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Create new commit (toCommit)
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "another-folder/file.txt", "content", "Commit in another folder")

			// ACT
			// Search only in app-manifests folder
			changes, err := provider.GetChangedFilesRaw(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(BeEmpty())
		})
	})

	Context("when a manifest file is deleted", func() {
		It("should return one FileChange with a Deleted status", func() {
			// SETUP
			// Create new commit with manifest
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			content := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: my-app, namespace: my-ns}}"
			fromCommitHash := makeCommit(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", content, "Add deployment")

			// Delete manifest and commit
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Remove("app-manifests/deployment.yaml")
			Expect(err).NotTo(HaveOccurred())
			toCommitHash, err := worktree.Commit("Delete deployment", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, fromCommitHash, toCommitHash.String(), "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))

			change, exists := changes["app-manifests/deployment.yaml"]
			Expect(exists).To(BeTrue())
			Expect(change.Status).To(Equal(dto.Deleted))
			Expect(change.Path).To(Equal("app-manifests/deployment.yaml"))
			// Content should be the deleted file's content
			Expect(string(change.Content)).To(Equal(content))
		})
	})

	Context("when a manifest file is updated", func() {
		It("should return one FileChange with correct content and status", func() {
			// SETUP
			initialContent := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: my-app, namespace: my-ns}}"
			updatedContent := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: my-app, namespace: my-ns}, spec: {replicas: 3}}"
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", initialContent, "Add deployment")
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", updatedContent, "Update deployment")

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))

			change, exists := changes["app-manifests/deployment.yaml"]
			Expect(exists).To(BeTrue())
			Expect(change.Status).To(Equal(dto.Updated))
			Expect(change.Path).To(Equal("app-manifests/deployment.yaml"))
			Expect(string(change.Content)).To(Equal(updatedContent))
		})
	})

	Context("when a manifest file is created", func() {
		It("should return one FileChange with correct content and status", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			newContent := "{apiVersion: v1, kind: Service, metadata: {name: my-service, namespace: my-ns}}"
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/service.yaml", newContent, "Add service")

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))

			change, exists := changes["app-manifests/service.yaml"]
			Expect(exists).To(BeTrue())
			Expect(change.Status).To(Equal(dto.New))
			Expect(change.Path).To(Equal("app-manifests/service.yaml"))
			Expect(string(change.Content)).To(Equal(newContent))
		})
	})

	Context("when only non-yaml files are changed", func() {
		It("should return the changed file", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/README.txt", "docs", "Add docs")

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))
			change, exists := changes["app-manifests/README.txt"]
			Expect(exists).To(BeTrue())
			Expect(string(change.Content)).To(Equal("docs"))
		})
	})

	Context("with a complex mix of added, updated, deleted files", func() {
		It("should return all four changes", func() {
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			// Add 2 initial files
			makeCommit(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "{apiVersion: apps/v1, kind: Deployment}", "Add deployment")
			fromCommitHash := makeCommit(workspaceRepo, workspacePath, "app-manifests/service.yaml", "{apiVersion: v1, kind: Service}", "Add service")

			// Perform changes for toCommit
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())

			// Update deployment.yaml
			updatedDeployment := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: updated-app}}"
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/deployment.yaml"), []byte(updatedDeployment), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/deployment.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Delete service.yaml
			_, err = worktree.Remove("app-manifests/service.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Add configmap.yaml
			newConfigmap := "{apiVersion: v1, kind: ConfigMap, metadata: {name: new-config}}"
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/configmap.yaml"), []byte(newConfigmap), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/configmap.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Add a non-yaml file
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/notes.txt"), []byte("some notes"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/notes.txt")
			Expect(err).NotTo(HaveOccurred())

			// Commit all these changes
			toCommitHash, err := worktree.Commit("Complex update", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			changes, err := provider.GetChangedFilesRaw(ctx, fromCommitHash, toCommitHash.String(), "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(4))

			// Check changes have correct status types
			statuses := make(map[dto.FileChangeStatus]int)
			for _, c := range changes {
				statuses[c.Status]++
			}
			Expect(statuses[dto.New]).To(Equal(2))     // configmap.yaml and notes.txt
			Expect(statuses[dto.Updated]).To(Equal(1)) // deployment.yaml
			Expect(statuses[dto.Deleted]).To(Equal(1)) // service.yaml

			// Verify specific files
			configChange, exists := changes["app-manifests/configmap.yaml"]
			Expect(exists).To(BeTrue())
			Expect(configChange.Status).To(Equal(dto.New))
			Expect(string(configChange.Content)).To(Equal(newConfigmap))

			deployChange, exists := changes["app-manifests/deployment.yaml"]
			Expect(exists).To(BeTrue())
			Expect(deployChange.Status).To(Equal(dto.Updated))
			Expect(string(deployChange.Content)).To(Equal(updatedDeployment))
		})
	})
})

// generateTestPGPKey creates a test PGP entity with a private/public key pair
func generateTestPGPKey(name, email string) *openpgp.Entity {
	config := &packet.Config{
		DefaultHash: crypto.SHA256,
	}
	entity, err := openpgp.NewEntity(name, "", email, config)
	Expect(err).NotTo(HaveOccurred())
	return entity
}

// setupGovernanceFolderWithMSR creates a governance folder structure with MSR and signature
func setupGovernanceFolderWithMSR(repoPath, governancePath, msrName string, version int, msr *dto.ManifestSigningRequestManifestObject, pgpEntity *openpgp.Entity) {
	versionFolder := filepath.Join(repoPath, governancePath, fmt.Sprintf("v_%d", version))
	err := os.MkdirAll(versionFolder, 0755)
	Expect(err).NotTo(HaveOccurred())

	// Write MSR file
	msrBytes, err := yaml.Marshal(msr)
	Expect(err).NotTo(HaveOccurred())
	msrPath := filepath.Join(versionFolder, fmt.Sprintf("%s.yaml", msrName))
	err = os.WriteFile(msrPath, msrBytes, 0644)
	Expect(err).NotTo(HaveOccurred())

	// Create signature for MSR
	signature, err := cryptolib.CreateDetachedSignatureByEntity(msrBytes, pgpEntity)
	Expect(err).NotTo(HaveOccurred())
	sigPath := filepath.Join(versionFolder, fmt.Sprintf("%s.yaml.sig", msrName))
	err = os.WriteFile(sigPath, signature, 0644)
	Expect(err).NotTo(HaveOccurred())
}

// setupGovernanceFolderWithMCA creates a governance folder structure with MCA and signature
func setupGovernanceFolderWithMCA(repoPath, governancePath, mcaName string, version int, mca *dto.ManifestChangeApprovalManifestObject, pgpEntity *openpgp.Entity) {
	versionFolder := filepath.Join(repoPath, governancePath, fmt.Sprintf("v_%d", version))
	err := os.MkdirAll(versionFolder, 0755)
	Expect(err).NotTo(HaveOccurred())

	// Write MCA file
	mcaBytes, err := yaml.Marshal(mca)
	Expect(err).NotTo(HaveOccurred())
	mcaPath := filepath.Join(versionFolder, fmt.Sprintf("%s.yaml", mcaName))
	err = os.WriteFile(mcaPath, mcaBytes, 0644)
	Expect(err).NotTo(HaveOccurred())

	// Create signature for MCA
	signature, err := cryptolib.CreateDetachedSignatureByEntity(mcaBytes, pgpEntity)
	Expect(err).NotTo(HaveOccurred())
	sigPath := filepath.Join(versionFolder, fmt.Sprintf("%s.yaml.sig", mcaName))
	err = os.WriteFile(sigPath, signature, 0644)
	Expect(err).NotTo(HaveOccurred())
}

// addGovernorSignature adds a governor signature to an existing MSR
func addGovernorSignature(repoPath, governancePath, msrName string, version int, msrContent []byte, pgpEntity *openpgp.Entity) {
	versionFolder := filepath.Join(repoPath, governancePath, fmt.Sprintf("v_%d", version))
	signaturesFolder := filepath.Join(versionFolder, "signatures")
	err := os.MkdirAll(signaturesFolder, 0755)
	Expect(err).NotTo(HaveOccurred())

	// Create governor signature
	signature, err := cryptolib.CreateDetachedSignatureByEntity(msrContent, pgpEntity)
	Expect(err).NotTo(HaveOccurred())

	// Get public key hash for filename
	pubKeyHash, err := cryptolib.ConvertPublicKeyToHash(pgpEntity)
	Expect(err).NotTo(HaveOccurred())

	sigPath := filepath.Join(signaturesFolder, fmt.Sprintf("%s.yaml.sig.%s", msrName, pubKeyHash))
	err = os.WriteFile(sigPath, signature, 0644)
	Expect(err).NotTo(HaveOccurred())
}

// createSampleMSR creates a sample MSR for testing
func createSampleMSR(name string, version int) *dto.ManifestSigningRequestManifestObject {
	return &dto.ManifestSigningRequestManifestObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "manifests.governance.io/v1alpha1",
			Kind:       "ManifestSigningRequest",
		},
		ObjectMeta: dto.ManifestRef{
			Name:      name,
			Namespace: "default",
		},
		Spec: dto.ManifestSigningRequestSpec{
			Version:           version,
			CommitSHA:         "abc123",
			PreviousCommitSHA: "def456",
			MRT: dto.VersionedManifestRef{
				Name:      "test-mrt",
				Namespace: "default",
				Version:   1,
			},
			PublicKey:        "test-public-key",
			GitRepositoryURL: "https://github.com/test/repo",
			Locations: dto.Locations{
				GovernancePath: "governance",
				SourcePath:     "manifests",
			},
			Changes: []dto.FileChange{
				{
					Path:   "manifests/deployment.yaml",
					Status: dto.New,
				},
			},
			Governors: dto.GovernorList{
				Members: []dto.Governor{
					{Alias: "governor1", PublicKey: "key1"},
				},
			},
			Require: dto.ApprovalRule{
				AtLeast: intPtr(1),
			},
		},
	}
}

// createSampleMCA creates a sample MCA for testing
func createSampleMCA(name string, version int) *dto.ManifestChangeApprovalManifestObject {
	return &dto.ManifestChangeApprovalManifestObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "manifests.governance.io/v1alpha1",
			Kind:       "ManifestChangeApproval",
		},
		ObjectMeta: dto.ManifestRef{
			Name:      name,
			Namespace: "default",
		},
		Spec: dto.ManifestChangeApprovalSpec{
			Version:           version,
			CommitSHA:         "abc123",
			PreviousCommitSHA: "def456",
			MRT: dto.VersionedManifestRef{
				Name:      "test-mrt",
				Namespace: "default",
				Version:   1,
			},
			MSR: dto.VersionedManifestRef{
				Name:      "test-msr",
				Namespace: "default",
				Version:   version,
			},
			PublicKey:        "test-public-key",
			GitRepositoryURL: "https://github.com/test/repo",
			Locations: dto.Locations{
				GovernancePath: "governance",
				SourcePath:     "manifests",
			},
			Changes: []dto.FileChange{
				{
					Path:   "manifests/deployment.yaml",
					Status: dto.New,
				},
			},
			Governors: dto.GovernorList{
				Members: []dto.Governor{
					{Alias: "governor1", PublicKey: "key1"},
				},
			},
			Require: dto.ApprovalRule{
				AtLeast: intPtr(1),
			},
			CollectedSignatures: []dto.Signature{
				{Signer: "governor1"},
			},
		},
	}
}

func intPtr(i int) *int {
	return &i
}

var _ = Describe("gitProvider PushGovernorSignature Method", func() {
	var (
		remotePath       string
		workspacePath    string
		provider         *BaseGitProvider
		localPath        string
		ctx              context.Context
		pgpEntity        *openpgp.Entity
		userInfo         internalconfig.UserInfo
		governancePolicy *GovernancePolicy
	)

	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		// Generate test PGP key
		pgpEntity = generateTestPGPKey("Test Governor", "test@example.com")

		// Setup user info
		userInfo = internalconfig.UserInfo{
			Name:  "Test User",
			Email: "testuser@example.com",
		}

		// Setup governance policy
		governancePolicy = &GovernancePolicy{
			GovernancePath: "governance",
			MSRName:        "msr",
			MCAName:        "mca",
		}

		// Serialize PGP private key
		var buf bytes.Buffer
		writer, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
		Expect(err).NotTo(HaveOccurred())
		err = pgpEntity.SerializePrivate(writer, nil)
		Expect(err).NotTo(HaveOccurred())
		err = writer.Close()
		Expect(err).NotTo(HaveOccurred())
		pgpPrivateKeyArmored := buf.String()

		// Setup provider with PGP secrets
		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
			pgpSecrets: cryptolib.Secrets{
				PrivateKey: pgpPrivateKeyArmored,
				Passphrase: "",
			},
		}
	})

	Context("when successfully pushing a governor signature", func() {
		It("should create signature file and push to remote", func() {
			// SETUP
			// Create MSR to sign
			msr := createSampleMSR("test-msr", 0)
			msr.Spec.Locations.GovernancePath = governancePolicy.GovernancePath

			// Create initial governance structure in workspace
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 0, msr, pgpEntity)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MSR", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			commitHash, err := provider.PushGovernorSignature(ctx, msr, userInfo)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(commitHash).NotTo(BeEmpty())

			// Pull changes to workspace to verify signature file was pushed
			pullErr := worktree.Pull(&git.PullOptions{})
			Expect(pullErr).To(Or(BeNil(), Equal(git.NoErrAlreadyUpToDate)))

			// Verify signatures folder exists
			signaturesPath := filepath.Join(workspacePath, governancePolicy.GovernancePath, "v_0", "signatures")
			_, err = os.Stat(signaturesPath)
			if os.IsNotExist(err) {
				// If folder doesn't exist in workspace, list what's in v_0
				v0Path := filepath.Join(workspacePath, governancePolicy.GovernancePath, "v_0")
				v0Files, _ := os.ReadDir(v0Path)
				GinkgoWriter.Printf("Files in v_0: %v\n", v0Files)
				Fail("Signatures folder doesn't exist")
			}
			Expect(err).NotTo(HaveOccurred())

			// List files in signatures folder
			files, err := os.ReadDir(signaturesPath)
			Expect(err).NotTo(HaveOccurred())

			// Debug: print all filenames
			if len(files) == 0 {
				GinkgoWriter.Println("No files found in signatures folder")
			} else {
				for _, f := range files {
					GinkgoWriter.Printf("Found file: %s\n", f.Name())
				}
			}

			// Check that at least one signature file exists with the correct prefix
			// Note: signature files use the MSR's ObjectMeta.Name, not the governance policy MSRName
			expectedPrefix := fmt.Sprintf("%s.yaml.sig.", msr.ObjectMeta.Name)
			foundSig := false
			for _, file := range files {
				if !file.IsDir() && strings.HasPrefix(file.Name(), expectedPrefix) {
					foundSig = true
					break
				}
			}
			Expect(foundSig).To(BeTrue(), "Expected to find a governor signature file")
		})
	})

	Context("when MSR version folder doesn't exist yet", func() {
		It("should create the folder structure and signature", func() {
			// SETUP
			msr := createSampleMSR("test-msr", 5)
			msr.Spec.Locations.GovernancePath = governancePolicy.GovernancePath

			// ACT
			commitHash, err := provider.PushGovernorSignature(ctx, msr, userInfo)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(commitHash).NotTo(BeEmpty())

			// Verify folder structure was created
			versionFolder := filepath.Join(localPath, governancePolicy.GovernancePath, "v_5", "signatures")
			_, err = os.Stat(versionFolder)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("gitProvider GetLatestMSR Method", func() {
	var (
		remotePath       string
		workspacePath    string
		provider         *BaseGitProvider
		localPath        string
		ctx              context.Context
		pgpEntity        *openpgp.Entity
		governancePolicy *GovernancePolicy
	)

	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		pgpEntity = generateTestPGPKey("Test Signer", "signer@example.com")

		governancePolicy = &GovernancePolicy{
			GovernancePath: "governance",
			MSRName:        "msr",
			MCAName:        "mca",
		}

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when governance folder is empty", func() {
		It("should return an error", func() {
			// SETUP - create empty governance folder
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			govPath := filepath.Join(workspacePath, governancePolicy.GovernancePath)
			err = os.MkdirAll(govPath, 0755)
			Expect(err).NotTo(HaveOccurred())

			// Add .gitkeep so git can track the empty folder
			err = os.WriteFile(filepath.Join(govPath, ".gitkeep"), []byte(""), 0644)
			Expect(err).NotTo(HaveOccurred())

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add empty governance folder", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			msrInfo, err := provider.GetLatestMSR(ctx, governancePolicy)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("governance folder is empty"))
			Expect(msrInfo).To(BeNil())
		})
	})

	Context("when there is one versioned v_0 folder with MSR and its PGP signature", func() {
		It("should return the MSR with signature", func() {
			// SETUP
			msr := createSampleMSR(governancePolicy.MSRName, 0)
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 0, msr, pgpEntity)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MSR v_0", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			msrInfo, err := provider.GetLatestMSR(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(msrInfo).NotTo(BeNil())
			Expect(msrInfo.Obj.ObjectMeta.Name).To(Equal(governancePolicy.MSRName))
			Expect(msrInfo.Obj.Spec.Version).To(Equal(0))
			Expect(msrInfo.Content).NotTo(BeEmpty())
			Expect(msrInfo.Sign).NotTo(BeEmpty())
			Expect(msrInfo.GovernorsSigns).To(BeEmpty())
		})
	})

	Context("when there are multiple governance versioned folders v_0 and v_1 with MSR and their PGP signature", func() {
		It("should return the latest MSR from v_1", func() {
			// SETUP
			msr0 := createSampleMSR(governancePolicy.MSRName, 0)
			msr1 := createSampleMSR(governancePolicy.MSRName, 1)

			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 0, msr0, pgpEntity)
			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 1, msr1, pgpEntity)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MSR v_0 and v_1", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			msrInfo, err := provider.GetLatestMSR(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(msrInfo).NotTo(BeNil())
			Expect(msrInfo.Obj.Spec.Version).To(Equal(1))
			Expect(msrInfo.Sign).NotTo(BeEmpty())
			Expect(msrInfo.GovernorsSigns).To(BeEmpty())
		})
	})

	Context("when multiple versioned folders v_0 and v_1 with MSR, their PGP signature and governor signatures", func() {
		It("should return the latest MSR with 2 governor signatures", func() {
			// SETUP
			msr0 := createSampleMSR(governancePolicy.MSRName, 0)
			msr1 := createSampleMSR(governancePolicy.MSRName, 1)

			governor1 := generateTestPGPKey("Governor 1", "gov1@example.com")
			governor2 := generateTestPGPKey("Governor 2", "gov2@example.com")

			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			// Setup v_0
			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 0, msr0, pgpEntity)

			// Setup v_1 with governor signatures
			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 1, msr1, pgpEntity)

			msr1Bytes, err := yaml.Marshal(msr1)
			Expect(err).NotTo(HaveOccurred())
			addGovernorSignature(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 1, msr1Bytes, governor1)
			addGovernorSignature(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 1, msr1Bytes, governor2)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MSR with governor signatures", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			msrInfo, err := provider.GetLatestMSR(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(msrInfo).NotTo(BeNil())
			Expect(msrInfo.Obj.Spec.Version).To(Equal(1))
			Expect(msrInfo.Sign).NotTo(BeEmpty())
			Expect(msrInfo.GovernorsSigns).To(HaveLen(2))
		})
	})
})

var _ = Describe("gitProvider GetMCAHistory Method", func() {
	var (
		remotePath       string
		workspacePath    string
		provider         *BaseGitProvider
		localPath        string
		ctx              context.Context
		pgpEntity        *openpgp.Entity
		governancePolicy *GovernancePolicy
	)

	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		pgpEntity = generateTestPGPKey("Test Signer", "signer@example.com")

		governancePolicy = &GovernancePolicy{
			GovernancePath: "governance",
			MSRName:        "msr",
			MCAName:        "mca",
		}

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when governance folder is empty", func() {
		It("should return an empty list", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			govPath := filepath.Join(workspacePath, governancePolicy.GovernancePath)
			err = os.MkdirAll(govPath, 0755)
			Expect(err).NotTo(HaveOccurred())

			// Add .gitkeep so git can track the empty folder
			err = os.WriteFile(filepath.Join(govPath, ".gitkeep"), []byte(""), 0644)
			Expect(err).NotTo(HaveOccurred())

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add empty governance folder", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			mcaHistory, err := provider.GetMCAHistory(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(mcaHistory).To(BeEmpty())
		})
	})

	Context("when there is one versioned v_0 folder with MCA and its PGP signature", func() {
		It("should return a list with one MCA", func() {
			// SETUP
			mca := createSampleMCA(governancePolicy.MCAName, 0)
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			setupGovernanceFolderWithMCA(workspacePath, governancePolicy.GovernancePath, governancePolicy.MCAName, 0, mca, pgpEntity)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MCA v_0", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			mcaHistory, err := provider.GetMCAHistory(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(mcaHistory).To(HaveLen(1))
			Expect(mcaHistory[0].Obj.ObjectMeta.Name).To(Equal(governancePolicy.MCAName))
			Expect(mcaHistory[0].Obj.Spec.Version).To(Equal(0))
			Expect(mcaHistory[0].Content).NotTo(BeEmpty())
			Expect(mcaHistory[0].Sign).NotTo(BeEmpty())
		})
	})

	Context("when there are multiple governance versioned folders v_0 and v_1 with MCA and their PGP signature", func() {
		It("should return a list with both MCAs in order", func() {
			// SETUP
			mca0 := createSampleMCA(governancePolicy.MCAName, 0)
			mca1 := createSampleMCA(governancePolicy.MCAName, 1)

			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			setupGovernanceFolderWithMCA(workspacePath, governancePolicy.GovernancePath, governancePolicy.MCAName, 0, mca0, pgpEntity)
			setupGovernanceFolderWithMCA(workspacePath, governancePolicy.GovernancePath, governancePolicy.MCAName, 1, mca1, pgpEntity)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MCA v_0 and v_1", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			mcaHistory, err := provider.GetMCAHistory(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(mcaHistory).To(HaveLen(2))
			Expect(mcaHistory[0].Obj.Spec.Version).To(Equal(0))
			Expect(mcaHistory[1].Obj.Spec.Version).To(Equal(1))
		})
	})

	Context("when there are MSR files but no MCA files", func() {
		It("should return an empty list", func() {
			// SETUP
			msr := createSampleMSR(governancePolicy.MSRName, 0)
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			setupGovernanceFolderWithMSR(workspacePath, governancePolicy.GovernancePath, governancePolicy.MSRName, 0, msr, pgpEntity)

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add(".")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Add MSR only", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// ACT
			mcaHistory, err := provider.GetMCAHistory(ctx, governancePolicy)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(mcaHistory).To(BeEmpty())
		})
	})
})

var _ = Describe("gitProvider GetFileDiffPatchParts Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *BaseGitProvider
		localPath     string
		ctx           context.Context
	)

	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &BaseGitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when MSR changes match the actual git diff", func() {
		It("should return patch parts for all files in MSR", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			// Create initial commit
			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "initial content", "Initial commit")

			// Create new commit with changes
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "updated content", "Update deployment")

			// Create MSR with matching changes
			msr := &dto.ManifestSigningRequestManifestObject{
				Spec: dto.ManifestSigningRequestSpec{
					Changes: []dto.FileChange{
						{
							Path:   "app-manifests/deployment.yaml",
							Status: dto.Updated,
						},
					},
				},
			}

			// ACT
			patches, err := provider.GetFileDiffPatchParts(ctx, msr, fromCommitHash, toCommitHash)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(patches).To(HaveLen(1))
			Expect(patches).To(HaveKey("app-manifests/deployment.yaml"))
		})
	})

	Context("when MSR has multiple file changes", func() {
		It("should return patches for all matching files", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			// Create initial files
			makeCommit(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "deploy content", "Add deployment")
			fromCommitHash := makeCommit(workspaceRepo, workspacePath, "app-manifests/service.yaml", "service content", "Add service")

			// Update both files
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/deployment.yaml"), []byte("updated deploy"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/deployment.yaml")
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/service.yaml"), []byte("updated service"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/service.yaml")
			Expect(err).NotTo(HaveOccurred())

			toCommitHash, err := worktree.Commit("Update both", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())

			// Create MSR with both changes
			msr := &dto.ManifestSigningRequestManifestObject{
				Spec: dto.ManifestSigningRequestSpec{
					Changes: []dto.FileChange{
						{Path: "app-manifests/deployment.yaml", Status: dto.Updated},
						{Path: "app-manifests/service.yaml", Status: dto.Updated},
					},
				},
			}

			// ACT
			patches, err := provider.GetFileDiffPatchParts(ctx, msr, fromCommitHash, toCommitHash.String())

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(patches).To(HaveLen(2))
			Expect(patches).To(HaveKey("app-manifests/deployment.yaml"))
			Expect(patches).To(HaveKey("app-manifests/service.yaml"))
		})
	})

	Context("when MSR changes don't match the actual git diff", func() {
		It("should return an error", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "initial", "Initial")
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "updated", "Updated")

			// Create MSR with non-matching changes
			msr := &dto.ManifestSigningRequestManifestObject{
				Spec: dto.ManifestSigningRequestSpec{
					Changes: []dto.FileChange{
						{Path: "app-manifests/nonexistent.yaml", Status: dto.Updated},
					},
				},
			}

			// ACT
			patches, err := provider.GetFileDiffPatchParts(ctx, msr, fromCommitHash, toCommitHash)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("end file slice is"))
			Expect(patches).To(BeEmpty())
		})
	})

	Context("when MSR specifies status that doesn't match actual change type", func() {
		It("should not include that file and return an error", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "initial", "Initial")
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "updated", "Updated")

			// MSR says it's New, but it's actually Updated
			msr := &dto.ManifestSigningRequestManifestObject{
				Spec: dto.ManifestSigningRequestSpec{
					Changes: []dto.FileChange{
						{Path: "app-manifests/deployment.yaml", Status: dto.New},
					},
				},
			}

			// ACT
			patches, err := provider.GetFileDiffPatchParts(ctx, msr, fromCommitHash, toCommitHash)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(patches).To(BeEmpty())
		})
	})
})
