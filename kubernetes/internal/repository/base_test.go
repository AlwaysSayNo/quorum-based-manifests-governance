package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// makeCommit is a shortcut function that creates file by the given fileName and commits
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

var _ = Describe("BaseGitProvider Sync Method", func() {
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

var _ = Describe("BaseGitProvider GetLatestRevision Method", func() {
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

var _ = Describe("BaseGitProvider HasRevision Method", func() {
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

var _ = Describe("BaseGitProvider GetChangedFiles Method", func() {
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
			changes, _, err := provider.GetChangedFiles(ctx, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "app-manifests")

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
			changes, _, err := provider.GetChangedFiles(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", toCommitHash, "app-manifests")

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
			changes, _, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(BeEmpty())
		})
	})

	Context("when a manifest file is deleted", func() {
		It("should return one FileChange with a Deleted status and correct hash", func() {
			// SETUP
			// Create new commit with manifest
			deploymentYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-deployment
  namespace: default`
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", deploymentYAML, "Add deployment")

			// Get expected hash
			hasher := sha256.New()
			hasher.Write([]byte(deploymentYAML))
			expectedHash := hex.EncodeToString(hasher.Sum(nil))

			// Delete manifest and commit
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())
			err = os.Remove(filepath.Join(workspacePath, "app-manifests/deployment.yaml"))
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/deployment.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Commit("Delete deployment", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())
			toCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			changes, _, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))
			Expect(changes[0].Path).To(Equal("app-manifests/deployment.yaml"))
			Expect(changes[0].Status).To(Equal(governancev1alpha1.Deleted))
			Expect(changes[0].Kind).To(Equal("Deployment"))
			Expect(changes[0].Name).To(Equal("my-deployment"))
			Expect(changes[0].Namespace).To(Equal("default"))
			// Hash of the deleted file
			Expect(changes[0].SHA256).To(Equal(expectedHash))
		})
	})

	Context("when a manifest file is updated", func() {
		It("should return one FileChange with correct hash and metadata", func() {
			// SETUP
			serviceYAML := `apiVersion: v1
kind: Service
metadata:
  name: my-service
  namespace: default`
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/service.yaml", serviceYAML, "Add service")
			updatedServiceYAML := serviceYAML + "\nspec:\n  type: ClusterIP"
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/service.yaml", updatedServiceYAML, "Update service")

			// Get expected hash
			hasher := sha256.New()
			hasher.Write([]byte(updatedServiceYAML))
			expectedHash := hex.EncodeToString(hasher.Sum(nil))

			// ACT
			changes, _, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))
			Expect(changes[0].Path).To(Equal("app-manifests/service.yaml"))
			Expect(changes[0].Status).To(Equal(governancev1alpha1.Updated))
			Expect(changes[0].Kind).To(Equal("Service"))
			Expect(changes[0].Name).To(Equal("my-service"))
			Expect(changes[0].Namespace).To(Equal("default"))
			Expect(changes[0].SHA256).To(Equal(expectedHash))
		})
	})

	Context("when a manifest file is created", func() {
		It("should return one FileChange with correct hash and metadata", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			configMapYAML := `apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: default`
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/configmap.yaml", configMapYAML, "Add configmap")

			// Get expected hash
			hasher := sha256.New()
			hasher.Write([]byte(configMapYAML))
			expectedHash := hex.EncodeToString(hasher.Sum(nil))

			// ACT
			changes, _, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(1))
			Expect(changes[0].Path).To(Equal("app-manifests/configmap.yaml"))
			Expect(changes[0].Status).To(Equal(governancev1alpha1.New))
			Expect(changes[0].Kind).To(Equal("ConfigMap"))
			Expect(changes[0].Name).To(Equal("my-config"))
			Expect(changes[0].Namespace).To(Equal("default"))
			Expect(changes[0].SHA256).To(Equal(expectedHash))
		})
	})

	Context("when only non-manifest files are changed", func() {
		It("should return an empty list", func() {
			// SETUP
			fromCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			toCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/notes.txt", "not a manifest", "Add notes")

			// ACT
			changes, _, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(BeEmpty())
		})
	})

	Context("with a complex mix of added, updated, deleted, and non-manifest files", func() {
		It("should return only the three relevant manifest changes", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			// Add 2 initial files
			makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-deployment\n  namespace: default", "Add deployment")
			fromCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "app-manifests/service.yaml", "apiVersion: v1\nkind: Service\nmetadata:\n  name: my-service\n  namespace: default", "Add service")

			// Perform changes for toCommit
			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())

			// Update deployment.yaml
			updatedDeployment := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-deployment\n  namespace: default\nspec:\n  replicas: 3"
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/deployment.yaml"), []byte(updatedDeployment), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/deployment.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Delete service.yaml
			err = os.Remove(filepath.Join(workspacePath, "app-manifests/service.yaml"))
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/service.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Add configmap.yaml
			configMap := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: my-config\n  namespace: default"
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/configmap.yaml"), []byte(configMap), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/configmap.yaml")
			Expect(err).NotTo(HaveOccurred())

			// Add a non-manifest file
			err = os.WriteFile(filepath.Join(workspacePath, "app-manifests/notes.txt"), []byte("some notes"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("app-manifests/notes.txt")
			Expect(err).NotTo(HaveOccurred())

			// Commit all these changes
			_, err = worktree.Commit("Complex changes", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(workspaceRepo.Push(&git.PushOptions{})).To(Succeed())
			toCommitHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			changes, _, err := provider.GetChangedFiles(ctx, fromCommitHash, toCommitHash, "app-manifests")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(changes).To(HaveLen(3)) // Should ignore notes.txt

			// Check changes have one of each status type
			statusCount := make(map[governancev1alpha1.FileChangeStatus]int)
			for _, change := range changes {
				statusCount[change.Status]++
			}
			Expect(statusCount[governancev1alpha1.New]).To(Equal(1))
			Expect(statusCount[governancev1alpha1.Updated]).To(Equal(1))
			Expect(statusCount[governancev1alpha1.Deleted]).To(Equal(1))

			// Check configmap.yaml is created
			configMapChange := changes[0]
			if configMapChange.Status != governancev1alpha1.New {
				configMapChange = changes[1]
				if configMapChange.Status != governancev1alpha1.New {
					configMapChange = changes[2]
				}
			}
			Expect(configMapChange.Kind).To(Equal("ConfigMap"))
			Expect(configMapChange.Name).To(Equal("my-config"))
		})
	})
})

var _ = Describe("BaseGitProvider PushMSR Method", func() {
	var (
		remotePath         string
		workspacePath      string
		provider           *BaseGitProvider
		localPath          string
		ctx                context.Context
		dummyMSR           *governancev1alpha1.ManifestSigningRequest
		dummyRepositoryMSR *governancev1alpha1.ManifestSigningRequestManifestObject
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

		dummyMSR = &governancev1alpha1.ManifestSigningRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "test-msr", Namespace: "test-ns"},
			Spec: governancev1alpha1.ManifestSigningRequestSpec{
				Version:   1,
				Locations: governancev1alpha1.Locations{GovernancePath: "governance"},
			},
		}

		dummyRepositoryMSR = &governancev1alpha1.ManifestSigningRequestManifestObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       dummyMSR.Kind,
				APIVersion: dummyMSR.APIVersion,
			},
			ObjectMeta: governancev1alpha1.ManifestRef{Name: dummyMSR.Name, Namespace: dummyMSR.Namespace},
			Spec:       *dummyMSR.Spec.DeepCopy(),
		}

		// Governance folder in the workspace for writing files
		Expect(os.MkdirAll(filepath.Join(workspacePath, "governance"), 0755)).To(Succeed())
	})

	Context("when the PGP private key is not configured", func() {
		It("should return a 'not configured' error", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushMSR(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PGP private key is not configured"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when the PGP private key is malformed", func() {
		It("should fail to read the armored key ring", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nBAD KEY DATA\n-----END PGP PRIVATE KEY BLOCK-----",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushMSR(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("illegal base64 data"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when using a valid, unprotected PGP key", func() {
		It("should successfully create, sign, commit, and push the MSR and signature files", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testNonEncryptedPgpPrivateKey,
			}

			// ACT
			pushedCommitHash, err := provider.PushMSR(ctx, dummyRepositoryMSR)

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

			msrPath := filepath.Join("governance", "v_1", "test-msr.yaml")
			sigPath := filepath.Join("governance", "v_1", "test-msr.yaml.sig")

			_, err = tree.File(msrPath)
			Expect(err).NotTo(HaveOccurred(), "MSR file should exist in the commit")
			_, err = tree.File(sigPath)
			Expect(err).NotTo(HaveOccurred(), "Signature file should exist in the commit")
		})
	})

	Context("when using a valid, protected PGP key with the correct passphrase", func() {
		It("should successfully decrypt the key and push the commit", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testEncryptedPgpPrivateKey,
				Passphrase: testEncryptedPgpPassphrase,
			}

			// ACT
			pushedCommitHash, err := provider.PushMSR(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			latestRevision, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestRevision).To(Equal(pushedCommitHash))
		})
	})
})

var _ = Describe("BaseGitProvider PushMCA Method", func() {
	var (
		remotePath         string
		workspacePath      string
		provider           *BaseGitProvider
		localPath          string
		ctx                context.Context
		dummyMCA           *governancev1alpha1.ManifestChangeApproval
		dummyRepositoryMCA *governancev1alpha1.ManifestChangeApprovalManifestObject
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

		dummyMCA = &governancev1alpha1.ManifestChangeApproval{
			ObjectMeta: metav1.ObjectMeta{Name: "test-mca", Namespace: "test-ns"},
			Spec: governancev1alpha1.ManifestChangeApprovalSpec{
				Version:   1,
				Locations: governancev1alpha1.Locations{GovernancePath: "governance"},
			},
		}

		dummyRepositoryMCA = &governancev1alpha1.ManifestChangeApprovalManifestObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       dummyMCA.Kind,
				APIVersion: dummyMCA.APIVersion,
			},
			ObjectMeta: governancev1alpha1.ManifestRef{Name: dummyMCA.Name, Namespace: dummyMCA.Namespace},
			Spec:       *dummyMCA.Spec.DeepCopy(),
		}

		// Governance folder in the workspace for writing files
		Expect(os.MkdirAll(filepath.Join(workspacePath, "governance"), 0755)).To(Succeed())
	})

	Context("when the PGP private key is not configured", func() {
		It("should return a 'not configured' error", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushMCA(ctx, dummyRepositoryMCA)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PGP private key is not configured"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when the PGP private key is malformed", func() {
		It("should fail to read the armored key ring", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nBAD KEY DATA\n-----END PGP PRIVATE KEY BLOCK-----",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushMCA(ctx, dummyRepositoryMCA)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("illegal base64 data"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when using a valid, unprotected PGP key", func() {
		It("should successfully create, sign, commit, and push the MCA and signature files", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testNonEncryptedPgpPrivateKey,
			}

			// ACT
			pushedCommitHash, err := provider.PushMCA(ctx, dummyRepositoryMCA)

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

			mcaPath := filepath.Join("governance", "v_1", "test-mca.yaml")
			sigPath := filepath.Join("governance", "v_1", "test-mca.yaml.sig")

			_, err = tree.File(mcaPath)
			Expect(err).NotTo(HaveOccurred(), "MCA file should exist in the commit")
			_, err = tree.File(sigPath)
			Expect(err).NotTo(HaveOccurred(), "Signature file should exist in the commit")
		})
	})

	Context("when using a valid, protected PGP key with the correct passphrase", func() {
		It("should successfully decrypt the key and push the commit", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testEncryptedPgpPrivateKey,
				Passphrase: testEncryptedPgpPassphrase,
			}

			// ACT
			pushedCommitHash, err := provider.PushMCA(ctx, dummyRepositoryMCA)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			latestRevision, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestRevision).To(Equal(pushedCommitHash))
		})
	})
})

var _ = Describe("BaseGitProvider PushSummaryFile Method", func() {
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

		// Governance folder in the workspace for writing files
		Expect(os.MkdirAll(filepath.Join(workspacePath, "governance"), 0755)).To(Succeed())
	})

	Context("when the PGP private key is not configured", func() {
		It("should return a 'not configured' error", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushSummaryFile(ctx, "test content", "summary-file", "governance", 1)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PGP private key is not configured"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when the PGP private key is malformed", func() {
		It("should fail to read the armored key ring", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nBAD KEY DATA\n-----END PGP PRIVATE KEY BLOCK-----",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushSummaryFile(ctx, "test content", "summary-file", "governance", 1)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("illegal base64 data"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when using a valid, unprotected PGP key", func() {
		It("should successfully create, sign, commit, and push the summary file and signature", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testNonEncryptedPgpPrivateKey,
			}

			summaryContent := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-summary"

			// ACT
			pushedCommitHash, err := provider.PushSummaryFile(ctx, summaryContent, "summary-file", "governance", 1)

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

			summaryPath := filepath.Join("governance", "v_1", "summary-file.yaml")
			sigPath := filepath.Join("governance", "v_1", "summary-file.yaml.sig")

			_, err = tree.File(summaryPath)
			Expect(err).NotTo(HaveOccurred(), "Summary file should exist in the commit")
			_, err = tree.File(sigPath)
			Expect(err).NotTo(HaveOccurred(), "Signature file should exist in the commit")
		})
	})

	Context("when using a valid, protected PGP key with the correct passphrase", func() {
		It("should successfully decrypt the key and push the commit", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testEncryptedPgpPrivateKey,
				Passphrase: testEncryptedPgpPassphrase,
			}

			summaryContent := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-summary"

			// ACT
			pushedCommitHash, err := provider.PushSummaryFile(ctx, summaryContent, "summary-file", "governance", 1)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			latestRevision, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestRevision).To(Equal(pushedCommitHash))
		})
	})
})

var _ = Describe("BaseGitProvider PushGovernorSignature Method", func() {
	var (
		remotePath         string
		workspacePath      string
		provider           *BaseGitProvider
		localPath          string
		ctx                context.Context
		dummyMSR           *governancev1alpha1.ManifestSigningRequest
		dummyRepositoryMSR *governancev1alpha1.ManifestSigningRequestManifestObject
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

		dummyMSR = &governancev1alpha1.ManifestSigningRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "test-msr", Namespace: "test-ns"},
			Spec: governancev1alpha1.ManifestSigningRequestSpec{
				Version:   1,
				Locations: governancev1alpha1.Locations{GovernancePath: "governance"},
			},
		}

		dummyRepositoryMSR = &governancev1alpha1.ManifestSigningRequestManifestObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       dummyMSR.Kind,
				APIVersion: dummyMSR.APIVersion,
			},
			ObjectMeta: governancev1alpha1.ManifestRef{Name: dummyMSR.Name, Namespace: dummyMSR.Namespace},
			Spec:       *dummyMSR.Spec.DeepCopy(),
		}

		// Governance folder in the workspace for writing files
		Expect(os.MkdirAll(filepath.Join(workspacePath, "governance"), 0755)).To(Succeed())
	})

	Context("when the PGP private key is not configured", func() {
		It("should return a 'not configured' error", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushGovernorSignature(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("PGP private key is not configured"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when the PGP private key is malformed", func() {
		It("should fail to read the armored key ring", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nBAD KEY DATA\n-----END PGP PRIVATE KEY BLOCK-----",
				Passphrase: "",
			}

			// ACT
			commit, err := provider.PushGovernorSignature(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("illegal base64 data"))
			Expect(commit).To(BeEmpty())
		})
	})

	Context("when using a valid, unprotected PGP key", func() {
		It("should successfully create and push the governor signature file", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testNonEncryptedPgpPrivateKey,
			}

			// ACT
			pushedCommitHash, err := provider.PushGovernorSignature(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			// Verify the commit and signature file exist on the remote repo
			remoteRepo, err := git.PlainOpen(remotePath)
			Expect(err).NotTo(HaveOccurred())

			_, err = remoteRepo.CommitObject(plumbing.NewHash(pushedCommitHash))
			Expect(err).NotTo(HaveOccurred())

			commitObj, err := remoteRepo.CommitObject(plumbing.NewHash(pushedCommitHash))
			Expect(err).NotTo(HaveOccurred())
			tree, err := commitObj.Tree()
			Expect(err).NotTo(HaveOccurred())

			// The signature file name includes the public key hash, so we check the signatures folder exists
			signaturesPath := filepath.Join("governance", "v_1", "signatures")
			signaturesTree, err := tree.Tree(signaturesPath)
			Expect(err).NotTo(HaveOccurred(), "Signatures folder should exist")
			Expect(len(signaturesTree.Entries)).To(BeNumerically(">", 0), "At least one signature file should exist")
		})
	})

	Context("when using a valid, protected PGP key with the correct passphrase", func() {
		It("should successfully decrypt the key and push the governor signature", func() {
			// SETUP
			provider.pgpSecrets = PgpSecrets{
				PrivateKey: testEncryptedPgpPrivateKey,
				Passphrase: testEncryptedPgpPassphrase,
			}

			// ACT
			pushedCommitHash, err := provider.PushGovernorSignature(ctx, dummyRepositoryMSR)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(pushedCommitHash).NotTo(BeEmpty())

			latestRevision, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestRevision).To(Equal(pushedCommitHash))
		})
	})
})

var _ = Describe("BaseGitProvider IsNotAfter Method", func() {
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

	Context("when child commit is after ancestor commit", func() {
		It("should return false", func() {
			// SETUP
			// Get the initial commit as ancestor
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			ancestorCommit := headRef.Hash().String()

			// Create a new commit as child (after ancestor)
			childCommit := makeCommitAndPush(workspaceRepo, workspacePath, "new-file.txt", "new content", "New commit")

			// ACT
			isNotAfter, err := provider.IsNotAfter(ctx, ancestorCommit, childCommit)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(isNotAfter).To(BeFalse(), "child is after ancestor, so IsNotAfter should return false")
		})
	})

	Context("when child commit is the same as ancestor commit", func() {
		It("should return true", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			sameCommit := headRef.Hash().String()

			// ACT
			isNotAfter, err := provider.IsNotAfter(ctx, sameCommit, sameCommit)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(isNotAfter).To(BeTrue(), "when commits are the same, IsNotAfter should return true")
		})
	})

	Context("when child commit is before ancestor commit", func() {
		It("should return true", func() {
			// SETUP
			// Get the initial commit as child
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			childCommit := headRef.Hash().String()

			// Create a new commit as ancestor (after child in history)
			ancestorCommit := makeCommitAndPush(workspaceRepo, workspacePath, "new-file.txt", "new content", "New commit")

			// ACT
			isNotAfter, err := provider.IsNotAfter(ctx, ancestorCommit, childCommit)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(isNotAfter).To(BeTrue(), "child is before ancestor, so IsNotAfter should return true")
		})
	})

	Context("when child commit is on a divergent branch", func() {
		It("should return true", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			worktree, err := workspaceRepo.Worktree()
			Expect(err).NotTo(HaveOccurred())

			// Get the current branch name (could be "main" or "master")
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			mainBranch := headRef.Name()

			// Create and checkout a feature branch from the current commit
			branchName := plumbing.NewBranchReferenceName("feature-branch")
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: branchName,
				Create: true,
			})
			Expect(err).NotTo(HaveOccurred())

			// Create a commit on the feature branch
			err = os.WriteFile(filepath.Join(workspacePath, "feature.txt"), []byte("feature data"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("feature.txt")
			Expect(err).NotTo(HaveOccurred())
			childCommitHash, err := worktree.Commit("Feature commit", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())
			childCommit := childCommitHash.String()

			// Switch back to main and create a new commit there
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: mainBranch,
			})
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(workspacePath, "main.txt"), []byte("main data"), 0644)
			Expect(err).NotTo(HaveOccurred())
			_, err = worktree.Add("main.txt")
			Expect(err).NotTo(HaveOccurred())
			ancestorCommitHash, err := worktree.Commit("Main commit", &git.CommitOptions{
				Author: &object.Signature{Name: "Test", Email: "test@example.com"},
			})
			Expect(err).NotTo(HaveOccurred())
			ancestorCommit := ancestorCommitHash.String()

			// Push both branches to remote
			Expect(workspaceRepo.Push(&git.PushOptions{
				RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("+%s:%s", mainBranch, mainBranch))},
			})).To(Succeed())
			Expect(workspaceRepo.Push(&git.PushOptions{
				RefSpecs: []config.RefSpec{"+refs/heads/feature-branch:refs/heads/feature-branch"},
			})).To(Succeed())

			// ACT
			// Check if feature branch commit is not after the main branch commit
			// Since they diverged from a common ancestor, neither is after the other
			isNotAfter, err := provider.IsNotAfter(ctx, ancestorCommit, childCommit)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(isNotAfter).To(BeTrue(), "child on divergent branch is not after ancestor on different branch")
		})
	})

	Context("when ancestor commit does not exist", func() {
		It("should return false and no error", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			childCommit := headRef.Hash().String()

			fakeAncestor := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

			// ACT
			isNotAfter, err := provider.IsNotAfter(ctx, fakeAncestor, childCommit)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(isNotAfter).To(BeFalse())
		})
	})

	Context("when child commit does not exist", func() {
		It("should return false and no error", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := workspaceRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			ancestorCommit := headRef.Hash().String()

			fakeChild := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

			// ACT
			isNotAfter, err := provider.IsNotAfter(ctx, ancestorCommit, fakeChild)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(isNotAfter).To(BeFalse())
		})
	})
})

var _ = Describe("BaseGitProvider GetRemoteHeadCommit Method", func() {
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

	Context("when the remote repository exists and is healthy", func() {
		It("should return the correct HEAD commit hash", func() {
			// SETUP
			// Get expected HEAD from the remote repo directly
			remoteRepo, err := git.PlainOpen(remotePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := remoteRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			expectedHash := headRef.Hash().String()

			// ACT
			actualHash, err := provider.GetRemoteHeadCommit(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(actualHash).To(Equal(expectedHash))
		})
	})

	Context("when a new commit is pushed to the remote", func() {
		It("should return the new HEAD commit hash", func() {
			// SETUP
			// Push a new commit from workspace
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			newCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "new-file.txt", "new content", "New commit")

			// ACT
			actualHash, err := provider.GetRemoteHeadCommit(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(actualHash).To(Equal(newCommitHash))
		})
	})

	Context("when the remote URL is invalid", func() {
		It("should return an error", func() {
			// SETUP
			badProvider := &BaseGitProvider{
				remoteURL: "/invalid/path/to/remote.git",
				localPath: localPath,
				logger:    GinkgoLogr,
			}

			// ACT
			hash, err := badProvider.GetRemoteHeadCommit(ctx)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("list remotes"))
			Expect(hash).To(BeEmpty())
		})
	})

	Context("when the remote repository is empty", func() {
		It("should return an error about HEAD not found", func() {
			// SETUP
			// Create an empty bare repository
			emptyRemotePath := filepath.Join(GinkgoT().TempDir(), "empty-remote.git")
			_, err := git.PlainInit(emptyRemotePath, true)
			Expect(err).NotTo(HaveOccurred())

			emptyProvider := &BaseGitProvider{
				remoteURL: emptyRemotePath,
				localPath: localPath,
				logger:    GinkgoLogr,
			}

			// ACT
			hash, err := emptyProvider.GetRemoteHeadCommit(ctx)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("remote repository is empty"))
			Expect(hash).To(BeEmpty())
		})
	})
})

var _ = Describe("BaseGitProvider GetLocalHeadCommit Method", func() {
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

	Context("when the repository has been synced", func() {
		It("should return the correct local HEAD commit hash", func() {
			// SETUP
			err := provider.Sync(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Get expected HEAD from provider's local repo
			expectedHash, err := provider.GetLatestRevision(ctx)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			actualHash, err := provider.GetLocalHeadCommit(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(actualHash).To(Equal(expectedHash))
		})
	})

	Context("when the repository has not been initialized", func() {
		It("should return an error", func() {
			// SETUP
			// provider.repo is nil

			// ACT
			hash, err := provider.GetLocalHeadCommit(ctx)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository has not been initialized"))
			Expect(hash).To(BeEmpty())
		})
	})

	Context("when local repository is behind remote", func() {
		It("should return the local HEAD, not the remote HEAD", func() {
			// SETUP
			// First sync to initialize the local repo
			err := provider.Sync(ctx)
			Expect(err).NotTo(HaveOccurred())

			localHeadBeforePush, err := provider.GetLocalHeadCommit(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Push a new commit to remote from workspace
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			makeCommitAndPush(workspaceRepo, workspacePath, "new-file.txt", "new content", "New commit")

			// ACT
			// Get local HEAD without syncing
			localHeadAfterPush, err := provider.GetLocalHeadCommit(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(localHeadAfterPush).To(Equal(localHeadBeforePush), "local HEAD should not change without sync")

			// Verify remote HEAD is different
			remoteHead, err := provider.GetRemoteHeadCommit(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(remoteHead).NotTo(Equal(localHeadAfterPush), "remote HEAD should be different after push")
		})
	})

	Context("when syncing after remote updates", func() {
		It("should return the updated local HEAD after sync", func() {
			// SETUP
			// First sync to initialize the local repo
			err := provider.Sync(ctx)
			Expect(err).NotTo(HaveOccurred())

			localHeadBefore, err := provider.GetLocalHeadCommit(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Push a new commit to remote from workspace
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())
			newCommitHash := makeCommitAndPush(workspaceRepo, workspacePath, "new-file.txt", "new content", "New commit")

			// Sync to pull the new commit
			err = provider.Sync(ctx)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			localHeadAfter, err := provider.GetLocalHeadCommit(ctx)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(localHeadAfter).NotTo(Equal(localHeadBefore), "local HEAD should change after sync")
			Expect(localHeadAfter).To(Equal(newCommitHash), "local HEAD should match the new commit")
		})
	})
})

var _ = Describe("BaseGitProvider FetchMSRByVersion Method", func() {
	var (
		remotePath    string
		workspacePath string
		provider      *BaseGitProvider
		localPath     string
		ctx           context.Context
		dummyMSR      *governancev1alpha1.ManifestSigningRequest
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

		dummyMSR = &governancev1alpha1.ManifestSigningRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "test-msr", Namespace: "test-ns"},
			Spec: governancev1alpha1.ManifestSigningRequestSpec{
				Version:   1,
				Locations: governancev1alpha1.Locations{GovernancePath: "governance"},
			},
		}

		// Governance folder in the workspace
		Expect(os.MkdirAll(filepath.Join(workspacePath, "governance"), 0755)).To(Succeed())
	})

	Context("when the MSR file does not exist", func() {
		It("should return an error", func() {
			// SETUP - no MSR file created

			// ACT
			fetchedMSR, content, sig, govSigs, err := provider.FetchMSRByVersion(ctx, dummyMSR)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("open msr file"))
			Expect(fetchedMSR).To(BeNil())
			Expect(content).To(BeNil())
			Expect(sig).To(BeNil())
			Expect(govSigs).To(BeNil())
		})
	})

	Context("when the MSR signature file does not exist", func() {
		It("should return an error", func() {
			// SETUP
			// Create MSR file without signature
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			msrContent := `apiVersion: qubmango.io/v1alpha1
kind: ManifestSigningRequest
metadata:
  name: test-msr
  namespace: test-ns
spec:
  version: 1
  locations:
    governancePath: governance`

			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml", msrContent, "Add MSR without signature")

			// ACT
			fetchedMSR, content, sig, govSigs, err := provider.FetchMSRByVersion(ctx, dummyMSR)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("open msr signature file"))
			Expect(fetchedMSR).To(BeNil())
			Expect(content).To(BeNil())
			Expect(sig).To(BeNil())
			Expect(govSigs).To(BeNil())
		})
	})

	Context("when governance signature folder doesn't exist", func() {
		It("should return MSR with empty governor signatures", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			msrContent := `apiVersion: qubmango.io/v1alpha1
kind: ManifestSigningRequest
metadata:
  name: test-msr
  namespace: test-ns
spec:
  version: 1
  locations:
    governancePath: governance`

			sigContent := "-----BEGIN PGP SIGNATURE-----\ntest signature\n-----END PGP SIGNATURE-----"

			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml", msrContent, "Add MSR")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml.sig", sigContent, "Add MSR signature")

			// ACT
			fetchedMSR, content, sig, govSigs, err := provider.FetchMSRByVersion(ctx, dummyMSR)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMSR).NotTo(BeNil())
			Expect(fetchedMSR.ObjectMeta.Name).To(Equal("test-msr"))
			Expect(content).NotTo(BeEmpty())
			Expect(sig).NotTo(BeEmpty())
			Expect(govSigs).To(BeEmpty())
		})
	})

	Context("when governance signature folder is empty", func() {
		It("should return MSR with empty governor signatures", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			msrContent := `apiVersion: qubmango.io/v1alpha1
kind: ManifestSigningRequest
metadata:
  name: test-msr
  namespace: test-ns
spec:
  version: 1
  locations:
    governancePath: governance`

			sigContent := "-----BEGIN PGP SIGNATURE-----\ntest signature\n-----END PGP SIGNATURE-----"

			// Create empty signatures folder
			Expect(os.MkdirAll(filepath.Join(workspacePath, "governance/v_1/signatures"), 0755)).To(Succeed())

			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml", msrContent, "Add MSR")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml.sig", sigContent, "Add MSR signature")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/signatures/.gitkeep", "", "Add empty signatures folder")

			// ACT
			fetchedMSR, content, sig, govSigs, err := provider.FetchMSRByVersion(ctx, dummyMSR)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMSR).NotTo(BeNil())
			Expect(fetchedMSR.ObjectMeta.Name).To(Equal("test-msr"))
			Expect(content).NotTo(BeEmpty())
			Expect(sig).NotTo(BeEmpty())
			Expect(govSigs).To(BeEmpty())
		})
	})

	Context("when some governor signatures exist", func() {
		It("should return MSR with all governor signatures", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			msrContent := `apiVersion: qubmango.io/v1alpha1
kind: ManifestSigningRequest
metadata:
  name: test-msr
  namespace: test-ns
spec:
  version: 1
  locations:
    governancePath: governance`

			sigContent := "-----BEGIN PGP SIGNATURE-----\ntest signature\n-----END PGP SIGNATURE-----"
			govSig1 := "-----BEGIN PGP SIGNATURE-----\ngovernor1 signature\n-----END PGP SIGNATURE-----"
			govSig2 := "-----BEGIN PGP SIGNATURE-----\ngovernor2 signature\n-----END PGP SIGNATURE-----"

			// Create signatures folder
			Expect(os.MkdirAll(filepath.Join(workspacePath, "governance/v_1/signatures"), 0755)).To(Succeed())

			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml", msrContent, "Add MSR")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-msr.yaml.sig", sigContent, "Add MSR signature")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/signatures/test-msr.yaml.sig.abc123", govSig1, "Add gov sig 1")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/signatures/test-msr.yaml.sig.def456", govSig2, "Add gov sig 2")

			// ACT
			fetchedMSR, content, sig, govSigs, err := provider.FetchMSRByVersion(ctx, dummyMSR)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMSR).NotTo(BeNil())
			Expect(fetchedMSR.ObjectMeta.Name).To(Equal("test-msr"))
			Expect(content).NotTo(BeEmpty())
			Expect(sig).NotTo(BeEmpty())
			Expect(govSigs).To(HaveLen(2))
			Expect(string(govSigs[0])).To(Equal(govSig1))
			Expect(string(govSigs[1])).To(Equal(govSig2))
		})
	})
})

var _ = Describe("BaseGitProvider DeleteFolder Method", func() {
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
			pgpSecrets: PgpSecrets{
				PrivateKey: testNonEncryptedPgpPrivateKey,
			},
		}
	})

	Context("when the folder doesn't exist", func() {
		It("should succeed without error", func() {
			// SETUP - no folder created

			// ACT
			err := provider.DeleteFolder(ctx, "governance/v_1")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when the folder exists with one file", func() {
		It("should successfully delete the folder and commit", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			// Create a folder with one file
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/test-file.yaml", "test content", "Add test file")

			// Verify folder exists before deletion
			err = provider.Sync(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = os.Stat(filepath.Join(localPath, "governance/v_1/test-file.yaml"))
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = provider.DeleteFolder(ctx, "governance/v_1")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())

			// Verify deletion in remote repo
			remoteRepo, err := git.PlainOpen(remotePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := remoteRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			commit, err := remoteRepo.CommitObject(headRef.Hash())
			Expect(err).NotTo(HaveOccurred())
			tree, err := commit.Tree()
			Expect(err).NotTo(HaveOccurred())

			_, err = tree.Tree("governance/v_1")
			Expect(err).To(HaveOccurred(), "Folder should be deleted from the repository")
		})
	})

	Context("when the folder exists with complex structure (files + folders)", func() {
		It("should successfully delete the entire folder structure and commit", func() {
			// SETUP
			workspaceRepo, err := git.PlainOpen(workspacePath)
			Expect(err).NotTo(HaveOccurred())

			// Create a complex folder structure
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/msr.yaml", "msr content", "Add MSR")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/msr.yaml.sig", "sig content", "Add MSR sig")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/signatures/gov1.sig", "gov1 sig", "Add gov sig 1")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/signatures/gov2.sig", "gov2 sig", "Add gov sig 2")
			makeCommitAndPush(workspaceRepo, workspacePath, "governance/v_1/subfolder/data.yaml", "data content", "Add subfolder data")

			// Verify folder structure exists before deletion
			err = provider.Sync(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = os.Stat(filepath.Join(localPath, "governance/v_1"))
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = provider.DeleteFolder(ctx, "governance/v_1")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())

			// Verify deletion in remote repo
			remoteRepo, err := git.PlainOpen(remotePath)
			Expect(err).NotTo(HaveOccurred())
			headRef, err := remoteRepo.Head()
			Expect(err).NotTo(HaveOccurred())
			commit, err := remoteRepo.CommitObject(headRef.Hash())
			Expect(err).NotTo(HaveOccurred())
			tree, err := commit.Tree()
			Expect(err).NotTo(HaveOccurred())

			_, err = tree.Tree("governance/v_1")
			Expect(err).To(HaveOccurred(), "Entire folder structure should be deleted from the repository")

			// Verify local deletion
			_, err = os.Stat(filepath.Join(localPath, "governance/v_1"))
			Expect(os.IsNotExist(err)).To(BeTrue(), "Folder should be deleted from local path")
		})
	})
})
