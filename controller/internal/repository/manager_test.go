package repository

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/crypto/ssh"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type dummyGitFactory struct{}

func (d *dummyGitFactory) IdentifyProvider(repoURL string) bool {
	return strings.Contains(repoURL, "testhub.com")
}
func (d *dummyGitFactory) New(remoteURL, localPath string, auth transport.AuthMethod, pgpSecrets PgpSecrets) (GitRepository, error) {
	return &dummyGitRepo{}, nil
}

type dummyGitRepo struct{}

func (d *dummyGitRepo) Sync(ctx context.Context) error { return nil }
func (d *dummyGitRepo) HasRevision(ctx context.Context, commit string) (bool, error) {
	return true, nil
}
func (d *dummyGitRepo) GetLatestRevision(ctx context.Context) (string, error) { return "rev", nil }
func (d *dummyGitRepo) GetChangedFiles(ctx context.Context, fromCommit, toCommit string) ([]governancev1alpha1.FileChange, error) {
	return nil, nil
}
func (d *dummyGitRepo) PushMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (string, error) {
	return "", nil
}
func (d *dummyGitRepo) PushSignature(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, governorAlias string, signatureData []byte) (string, error) {
	return "", nil
}

var _ = Describe("Repository Manager", func() {

	const (
		MRTName                    = "test-mrt"
		ArgoCDApplicationName      = "test-argocd-application"
		ArgoCDApplicationNamespace = "argocd"
		SSHSecretName              = "test-git-creds"
		PGPSecretName              = "test-pgp-cred-name"
		TestRepoURL                = "https://testhub.com/TestUser/test-repo.git"
	)

	// These variables will be fresh for each "It" block
	var (
		ctx           context.Context
		testNamespace *corev1.Namespace
		manager       *Manager
	)

	BeforeEach(func() {
		ctx = context.Background()

		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-ns-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).Should(Succeed())

		manager = NewManager(k8sClient, GinkgoT().TempDir())
	})

	AfterEach(func() {
		// Clean up by deleting the namespace. Automatically garbage-collects all objects created inside it
		Expect(k8sClient.Delete(ctx, testNamespace)).Should(Succeed())
	})

	Context("when fetching a provider for an MRT", func() {
		It("should fail if MRT doesn't have Application in the cluster, associated with it", func() {
			// --- SETUP ---

			// Create a dummy MRT resource
			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: testNamespace.Name,
					},
				},
			}

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).Should(HaveOccurred())
			Expect(provider).Should(BeNil())
		})

		It("should fail if RepoURL is empty", func() {
			ctx := context.Background()

			// --- SETUP ---

			// Create dummy Application resource
			application := &argocdv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ArgoCDApplicationName,
					Namespace: testNamespace.Name,
				},

				Spec: argocdv1alpha1.ApplicationSpec{
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL: "",
					},
				},
			}
			Expect(k8sClient.Create(ctx, application)).Should(Succeed())

			// Create a dummy MRT resource
			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},

				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: ArgoCDApplicationNamespace,
					},
				},
			}

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).Should(HaveOccurred())
			Expect(provider).Should(BeNil())
		})

		It("should fail if provider list is empty", func() {
			// --- SETUP ---

			// Create dummy Application with a RepoURL
			application := &argocdv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ArgoCDApplicationName,
					Namespace: testNamespace.Name,
				},
				Spec: argocdv1alpha1.ApplicationSpec{
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL: TestRepoURL,
					},
				},
			}
			Expect(k8sClient.Create(ctx, application)).Should(Succeed())

			// Create MRT referencing that Application
			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: testNamespace.Name,
					},
				},
			}

			// Ensure manager has no providers → providerExists will be false
			manager.providers = nil

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no supported git provider for URL"))
			Expect(provider).Should(BeNil())
		})

		It("should fail if provider does not exist for the given RepoURL", func() {
			// --- SETUP ---

			// Create dummy Application with a RepoURL
			application := &argocdv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ArgoCDApplicationName,
					Namespace: testNamespace.Name,
				},
				Spec: argocdv1alpha1.ApplicationSpec{
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL: "https://nonexitstinghub.com/TestUser/test-repo.git",
					},
				},
			}
			Expect(k8sClient.Create(ctx, application)).Should(Succeed())

			// Create MRT referencing that Application
			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: testNamespace.Name,
					},
				},
			}

			// Inject dummy provider
			manager.providers = []GitRepositoryFactory{&dummyGitFactory{}}

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no supported git provider for URL"))
			Expect(provider).Should(BeNil())
		})

		It("should fail if pgpSecrets resource does not exist", func() {
			// --- SETUP ---

			application := &argocdv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ArgoCDApplicationName,
					Namespace: testNamespace.Name,
				},
				Spec: argocdv1alpha1.ApplicationSpec{
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL: TestRepoURL,
					},
				},
			}
			Expect(k8sClient.Create(ctx, application)).Should(Succeed())

			// Inject dummy provider
			manager.providers = []GitRepositoryFactory{&dummyGitFactory{}}

			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					PGPSecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: testNamespace.Name,
					},
				},
			}

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to fetch pgp secret"))
			Expect(provider).Should(BeNil())
		})

		It("should fail if pgpSecrets exists but sshSecrets missing", func() {
			// SETUP

			// Create Application
			application := &argocdv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ArgoCDApplicationName,
					Namespace: testNamespace.Name,
				},
				Spec: argocdv1alpha1.ApplicationSpec{
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL: TestRepoURL,
					},
				},
			}
			Expect(k8sClient.Create(ctx, application)).Should(Succeed())

			// Create PGP secret
			pgpSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PGPSecretName,
					Namespace: testNamespace.Name,
				},
				StringData: map[string]string{
					"privateKey": "FAKE_PGP_KEY",
					"passphrase": "FAKE_PASSPHRASE",
				},
			}
			Expect(k8sClient.Create(ctx, pgpSecret)).Should(Succeed())

			// Inject dummy provider
			manager.providers = []GitRepositoryFactory{&dummyGitFactory{}}

			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					SSHSecretsRef: governancev1alpha1.ManifestRef{
						Name:      SSHSecretName,
						Namespace: testNamespace.Name,
					},
					PGPSecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: testNamespace.Name,
					},
				},
			}

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to fetch git secret"))
			Expect(provider).Should(BeNil())
		})

		It("should correctly initialize a provider using secrets from the cluster", func() {
			// --- SETUP ---

			// Create Application
			application := &argocdv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ArgoCDApplicationName,
					Namespace: testNamespace.Name,
				},
				Spec: argocdv1alpha1.ApplicationSpec{
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL: TestRepoURL,
					},
				},
			}
			Expect(k8sClient.Create(ctx, application)).Should(Succeed())

			// Create SSH secret
			privateKey, _, err := generateTestSSHKey()
			Expect(err).NotTo(HaveOccurred())
			sshSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SSHSecretName,
					Namespace: testNamespace.Name,
				},
				StringData: map[string]string{
					"privateKey": privateKey,
					"passphrase": "",
				},
			}
			Expect(k8sClient.Create(ctx, sshSecret)).Should(Succeed())

			// Create PGP secret
			pgpSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PGPSecretName,
					Namespace: testNamespace.Name,
				},
				StringData: map[string]string{
					"privateKey": "FAKE_PGP_KEY",
					"passphrase": "FAKE_PASSPHRASE",
				},
			}
			Expect(k8sClient.Create(ctx, pgpSecret)).Should(Succeed())

			// Create MRT
			mrt := &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					SSHSecretsRef: governancev1alpha1.ManifestRef{
						Name:      SSHSecretName,
						Namespace: testNamespace.Name,
					},
					PGPSecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
						Name:      ArgoCDApplicationName,
						Namespace: testNamespace.Name,
					},
				},
			}

			// Inject dummy provider
			manager.providers = []GitRepositoryFactory{&dummyGitFactory{}}

			// --- ACT ---

			provider, err := manager.GetProviderForMRT(ctx, mrt)

			// --- VERIFY ---

			Expect(err).NotTo(HaveOccurred())
			Expect(provider).NotTo(BeNil())

			_, ok := provider.(*dummyGitRepo)
			Expect(ok).To(BeTrue())
		})
	})
})

func generateTestSSHKey() (string, string, error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}

	// Convert the private key to the OpenSSH PEM format
	pemBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return "", "", err
	}
	privKeyPem := string(pem.EncodeToMemory(pemBlock))

	// Get the public key in the authorized_keys format
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", err
	}
	pubKeyString := string(ssh.MarshalAuthorizedKey(sshPubKey))

	return privKeyPem, pubKeyString, nil
}
