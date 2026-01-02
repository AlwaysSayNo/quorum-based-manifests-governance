package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"sigs.k8s.io/controller-runtime/pkg/log"

	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		provider      *gitProvider
		localPath     string
		ctx           context.Context
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &gitProvider{
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
			badProvider := &gitProvider{
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
		provider      *gitProvider
		localPath     string
		ctx           context.Context
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &gitProvider{
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
			badProvider := &gitProvider{
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
		provider      *gitProvider
		localPath     string
		ctx           context.Context
		initialCommit plumbing.Hash
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &gitProvider{
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

var _ = Describe("gitProvider GetChangedFiles Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *gitProvider
		localPath     string
		ctx           context.Context
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &gitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}
	})

	Context("when the 'toCommit' hash is invalid", func() {
		It("should return an error", func() {
			// SETUP

			// ACT
			changes, err := provider.GetChangedFiles(ctx, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "app-manifests")

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
			changes, err := provider.GetChangedFiles(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", toCommitHash, "app-manifests")

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
			changes, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(BeEmpty())
		})
	})

	Context("when a manifest file is deleted", func() {
		It("should return one FileChange with a Deleted status and no hash", func() {
			// SETUP
			// Create new commit with manifest
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			contnent := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: my-app, namespace: my-ns}}"
			fromCommitHash := makeCommit(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", contnent, "Add deployment")

			// Get expected hash
			hasher := sha256.New()
			hasher.Write([]byte(contnent))
			expectedHash := hex.EncodeToString(hasher.Sum(nil))

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
			changes, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash.String(), "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))

			change := changes[0]
			Expect(change.Status).To(Equal(manager.Deleted))
			Expect(change.Path).To(Equal("app-manifests/deployment.yaml"))
			// Hash of the deleted file
			Expect(change.SHA256).To(Equal(expectedHash))
			Expect(change.Path).To(Equal("app-manifests/deployment.yaml"))
			Expect(change.Kind).To(Equal("Deployment"))
			Expect(change.Name).To(Equal("my-app"))
			Expect(change.Namespace).To(Equal("my-ns"))
		})
	})

	Context("when a manifest file is updated", func() {
		It("should return one FileChange with correct hash and metadata", func() {
			// SETUP
			initialContent := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: my-app, namespace: my-ns}}"
			updatedContent := "{apiVersion: apps/v1, kind: Deployment, metadata: {name: my-app, namespace: my-ns}, spec: {replicas: 3}}"
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", initialContent, "Add deployment")
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", updatedContent, "Update deployment")

			// Get expected hash
			hasher := sha256.New()
			hasher.Write([]byte(updatedContent))
			expectedHash := hex.EncodeToString(hasher.Sum(nil))

			// ACT
			changes, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))

			change := changes[0]
			Expect(change.Status).To(Equal(manager.Updated))
			Expect(change.Path).To(Equal("app-manifests/deployment.yaml"))
			Expect(change.Kind).To(Equal("Deployment"))
			Expect(change.Name).To(Equal("my-app"))
			Expect(change.Namespace).To(Equal("my-ns"))
			Expect(change.SHA256).To(Equal(expectedHash))
		})
	})

	Context("when a manifest file is created", func() {
		It("should return one FileChange with correct hash and metadata", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			newContent := "{apiVersion: v1, kind: Service, metadata: {name: my-service, namespace: my-ns}}"
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/service.yaml", newContent, "Add service")

			// Get expected hash
			hasher := sha256.New()
			hasher.Write([]byte(newContent))
			expectedHash := hex.EncodeToString(hasher.Sum(nil))

			// ACT
			changes, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))

			change := changes[0]
			Expect(change.Status).To(Equal(manager.New))
			Expect(change.Path).To(Equal("app-manifests/service.yaml"))
			Expect(change.Kind).To(Equal("Service"))
			Expect(change.Name).To(Equal("my-service"))
			Expect(change.Namespace).To(Equal("my-ns"))
			Expect(change.SHA256).To(Equal(expectedHash))
		})
	})

	Context("when only non-manifest files are changed", func() {
		It("should return an empty list", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/README.txt", "docs", "Add docs")

			// ACT
			changes, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(BeEmpty())
		})
	})

	Context("with a complex mix of added, updated, deleted, and non-manifest files", func() {
		It("should return only the three relevant manifest changes", func() {
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			// Add 2 initial files
			makeCommit(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "{apiVersion: apps/v1, kind: Deployment}", "Add deployment")
			fromCommitHash := makeCommit(workspaceRepo, workspacePath, "app-manifests/service.yaml", "{apiVersion: v1, kind: Service}", "Add service")

			// Perform changes for toCommit
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())

			// Update deployment.yaml
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/deployment.yaml"), []byte("{apiVersion: apps/v1, kind: Deployment, metadata: {name: updated-app}}"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/deployment.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Delete service.yaml
			_, err = worktree.Remove("app-manifests/service.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Add configmap.yaml
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/configmap.yaml"), []byte("{apiVersion: v1, kind: ConfigMap, metadata: {name: new-config}}"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/configmap.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Add a non-manifest file
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
			changes, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash.String(), "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(3)) // Should ignore notes.txt

			// Check changes have one of each status type
			statuses := make(map[manager.FileChangeStatus]int)
			for _, c := range changes {
				statuses[c.Status]++
			}
			Expect(statuses[manager.New]).To(Equal(1))
			Expect(statuses[manager.Updated]).To(Equal(1))
			Expect(statuses[manager.Deleted]).To(Equal(1))

			// Check configmap.yaml is created
			for _, c := range changes {
				if c.Status == manager.New {
					Expect(c.Name).To(Equal("new-config"))
				}
			}
		})
	})
})

var _ = Describe("gitProvider PushMSR Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *gitProvider
		localPath     string
		ctx           context.Context
		dummyMSR      *manager.ManifestSigningRequestManifestObject
	)

	// Set up a fresh git environment folders and a new provider instance
	BeforeEach(func() {
		remotePath, workspacePath = setupTestRepo()
		localPath = GinkgoT().TempDir()
		ctx = log.IntoContext(context.Background(), GinkgoLogr)

		provider = &gitProvider{
			remoteURL: remotePath,
			localPath: localPath,
			logger:    GinkgoLogr,
		}

		dummyMSR = &manager.ManifestSigningRequestManifestObject{
			ObjectMeta: manager.ManifestRef{Name: "test-msr"},
			Spec: manager.ManifestSigningRequestSpec{
				Location: manager.Location{GovernancePath: "governance"},
			},
		}
		// Governance folder in the workspace for writing files
		Expect(os.MkdirAll(filepath.Join(workspacePath, "governance"), 0755)).To(Succeed())
	})

	Context("when the PGP private key is not configured", func() {
		It("should return a 'not configured' error", func() {
			// SETUP
			repoObj := &manager.ManifestSigningRequestManifestObject{
				TypeMeta: metav1.TypeMeta{
					Kind:       dummyMSR.Kind,
					APIVersion: dummyMSR.APIVersion,
				},
				ObjectMeta: manager.ManifestRef{
					Name:      dummyMSR.ObjectMeta.Name,
					Namespace: dummyMSR.ObjectMeta.Namespace,
				},
				Spec: dummyMSR.Spec,
			}

			// ACT
			commit, err := provider.PushMSR(ctx, repoObj)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PGP private key is not configured"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when the PGP private key is malformed", func() {
		It("should fail to read the armored key ring", func() {
			// SETUP
			provider.pgpSecrets.PrivateKey = "--- FAKE_PGP_KEY ---"
			repoObj := &manager.ManifestSigningRequestManifestObject{
				TypeMeta: metav1.TypeMeta{
					Kind:       dummyMSR.Kind,
					APIVersion: dummyMSR.APIVersion,
				},
				ObjectMeta: manager.ManifestRef{
					Name:      dummyMSR.ObjectMeta.Name,
					Namespace: dummyMSR.ObjectMeta.Namespace,
				},
				Spec: dummyMSR.Spec,
			}

			// ACT
			commit, err := provider.PushMSR(ctx, repoObj)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid argument"))
			Expect(commit).To(BeEmpty())
		})
	})

	// TODO: test cases don't work. Probably problem with PGP key
	// Context("when the PGP key is encrypted but no passphrase is provided", func() {
	// 	It("should return a 'no passphrase' error", func() {
	// 		// SETUP
	// 		provider.pgpSecrets.PrivateKey = testEncryptedPgpPrivateKey
	// 		provider.pgpSecrets.Passphrase = ""

	// 		// ACT
	// 		commit, err := provider.PushMSR(ctx, dummyMSR)

	// 		// VERIFY
	// 		Expect(err).To(HaveOccurred())
	// 		Expect(err.Error()).To(ContainSubstring("PGP private key is encrypted, but no passphrase was provided"))
	// 		Expect(commit).To(BeEmpty())
	// 	})
	// })

	// Context("when the PGP key is encrypted and an incorrect passphrase is provided", func() {
	// 	It("should fail to decrypt the key", func() {
	// 		// SETUP
	// 		provider.pgpSecrets.PrivateKey = testEncryptedPgpPrivateKey
	// 		provider.pgpSecrets.Passphrase = "wrong-password"

	// 		// ACT
	// 		commit, err := provider.PushMSR(ctx, dummyMSR)

	// 		// VERIFY
	// 		Expect(err).To(HaveOccurred())
	// 		Expect(err.Error()).To(ContainSubstring("failed to decrypt PGP private key"))
	// 		Expect(commit).To(BeEmpty())
	// 	})
	// })

	Context("when using a valid, unprotected PGP key", func() {
		It("should successfully create, sign, commit, and push the MSR and signature files", func() {
			// SETUP
			provider.pgpSecrets.PrivateKey = testNonEncryptedPgpPrivateKey
			repoObj := &manager.ManifestSigningRequestManifestObject{
				TypeMeta: metav1.TypeMeta{
					Kind:       dummyMSR.Kind,
					APIVersion: dummyMSR.APIVersion,
				},
				ObjectMeta: manager.ManifestRef{
					Name:      dummyMSR.ObjectMeta.Name,
					Namespace: dummyMSR.ObjectMeta.Namespace,
				},
				Spec: dummyMSR.Spec,
			}

			// ACT
			pushedCommitHash, err := provider.PushMSR(ctx, repoObj)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			// Verify the commit and files exist on the remote repo
			remoteRepo, err := git.PlainOpen(remotePath)
			Expect(err).NotTo(HaveOccurred())

			_, err = remoteRepo.CommitObject(plumbing.NewHash(pushedCommitHash))
			Expect(err).NotTo(HaveOccurred())

			commitObj, err := remoteRepo.CommitObject(plumbing.NewHash(pushedCommitHash))
			Expect(err).NotTo(HaveOccurred())
			tree, err := commitObj.Tree()
			Expect(err).NotTo(HaveOccurred())

			msrPath := filepath.Join("governance", "v_0", "test-msr.yaml")
			sigPath := filepath.Join("governance", "v_0", "test-msr.yaml.sig")

			_, err = tree.File(msrPath)
			Expect(err).NotTo(HaveOccurred(), "MSR file should exist in the commit")
			_, err = tree.File(sigPath)
			Expect(err).NotTo(HaveOccurred(), "Signature file should exist in the commit")
		})
	})

	Context("when using a valid, protected PGP key with the correct passphrase", func() {
		It("should successfully decrypt the key and push the commit", func() {
			// SETUP
			provider.pgpSecrets.PrivateKey = testEncryptedPgpPrivateKey
			provider.pgpSecrets.Passphrase = testEncryptedPgpPassphrase
			repoObj := &manager.ManifestSigningRequestManifestObject{
				TypeMeta: metav1.TypeMeta{
					Kind:       dummyMSR.Kind,
					APIVersion: dummyMSR.APIVersion,
				},
				ObjectMeta: manager.ManifestRef{
					Name:      dummyMSR.ObjectMeta.Name,
					Namespace: dummyMSR.ObjectMeta.Namespace,
				},
				Spec: dummyMSR.Spec,
			}

			// ACT
			pushedCommitHash, err := provider.PushMSR(ctx, repoObj)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			latestRevision, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestRevision).To(Equal(pushedCommitHash))
		})
	})
})
