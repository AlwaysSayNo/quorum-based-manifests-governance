package repository_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"

	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
	managermocks "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository/mocks"
)

var _ = Describe("Repository Manager", func() {

	const (
		MRTName                    = "test-mrt"
		SSHSecretName              = "test-git-creds"
		PGPSecretName              = "test-pgp-cred-name"
		TestRepoURL                = "git@testhub.com:TestUser/test-repo.git"
	)

	var (
		ctx             context.Context
		mockCtrl        *gomock.Controller
		mockRepoFactory *managermocks.MockGitRepositoryFactory
		mockRepo        *managermocks.MockGitRepository
		testNamespace   *corev1.Namespace
		repoManager     *manager.Manager
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Setup
		mockCtrl = gomock.NewController(GinkgoT())
		mockRepoFactory = managermocks.NewMockGitRepositoryFactory(mockCtrl)
		mockRepo = managermocks.NewMockGitRepository(mockCtrl)

		mockRepoFactory.EXPECT().IdentifyProvider(gomock.Any()).Return(true).AnyTimes()
		mockRepoFactory.EXPECT().New(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()

		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-ns-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).Should(Succeed())

		repoManager = manager.NewManager(k8sClient, GinkgoT().TempDir())
	})

	AfterEach(func() {
		// Clean up by deleting the namespace. Automatically garbage-collects all objects created inside it
		Expect(k8sClient.Delete(ctx, testNamespace)).Should(Succeed())
	})

	It("should fail if RepoURL is empty", func() {
		// SETUP
		// Create a dummy MRT resource
		mrt := &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: testNamespace.Name,
			},

			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				GitRepository: governancev1alpha1.GitRepository{SSHURL: ""},
			},
		}

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("repository URL is not defined in MRT spec"))
		Expect(provider).Should(BeNil())
	})

	It("should fail if provider list is empty", func() {
		// SETUP

		// Create MRT
		mrt := &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// Ensure manager has no providers -> providerExists will be false
		// manager.providers = nil

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no supported git provider for URL"))
		Expect(provider).Should(BeNil())
	})

	It("should fail if pgpSecrets information is nil", func() {
		// SETUP

		// Inject dummy provider
		repoManager.Register(mockRepoFactory)

		mrt := &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("pgp information is nil"))
		Expect(provider).Should(BeNil())
	})

	It("should fail if pgpSecrets resource does not exist", func() {
		// SETUP

		// Inject dummy provider
		repoManager.Register(mockRepoFactory)

		mrt := &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					PublicKey: "FAKE_PGP_KEY",
				},
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to fetch pgp secret"))
		Expect(provider).Should(BeNil())
	})

	It("should fail if pgpSecrets exists but sshSecrets missing", func() {
		// SETUP

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
		repoManager.Register(mockRepoFactory)

		mrt := &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				SSH: &governancev1alpha1.SSHPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      SSHSecretName,
						Namespace: testNamespace.Name,
					},
				},
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					PublicKey: "FAKE_PGP_KEY",
				},
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to fetch git secret"))
		Expect(provider).Should(BeNil())
	})

	It("should fail if pgpSecrets exists but sshSecrets is nil", func() {
		// SETUP

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
		repoManager.Register(mockRepoFactory)

		mrt := &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					PublicKey: "FAKE_PGP_KEY",
				},
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ssh information is nil"))
		Expect(provider).Should(BeNil())
	})

	It("should fail if provider does not exist for the given RepoURL", func() {
		// SETUP

		// Create SSH secret
		privateKey, _, err := generateTestSSHKey()
		Expect(err).NotTo(HaveOccurred())
		sshSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      SSHSecretName,
				Namespace: testNamespace.Name,
			},
			StringData: map[string]string{
				"ssh-privatekey": privateKey,
				"passphrase":     "",
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
				SSH: &governancev1alpha1.SSHPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      SSHSecretName,
						Namespace: testNamespace.Name,
					},
				},
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					PublicKey: "FAKE_PGP_KEY",
				},
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// Inject dummy provider
		mockRepoFactory = managermocks.NewMockGitRepositoryFactory(mockCtrl)
		mockRepoFactory.EXPECT().IdentifyProvider(gomock.Any()).Return(false).AnyTimes()
		repoManager.Register(mockRepoFactory)

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no supported git provider for URL"))
		Expect(provider).Should(BeNil())
	})

	It("should correctly initialize a provider using secrets from the cluster", func() {
		// SETUP

		// Create SSH secret
		privateKey, _, err := generateTestSSHKey()
		Expect(err).NotTo(HaveOccurred())
		sshSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      SSHSecretName,
				Namespace: testNamespace.Name,
			},
			StringData: map[string]string{
				"ssh-privatekey": privateKey,
				"passphrase":     "",
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
				SSH: &governancev1alpha1.SSHPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      SSHSecretName,
						Namespace: testNamespace.Name,
					},
				},
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      PGPSecretName,
						Namespace: testNamespace.Name,
					},
					PublicKey: "FAKE_PGP_KEY",
				},
				GitRepository: governancev1alpha1.GitRepository{SSHURL: TestRepoURL},
			},
		}

		// Inject dummy provider
		repoManager.Register(mockRepoFactory)

		// ACT

		provider, err := repoManager.GetProviderForMRT(ctx, mrt)

		// VERIFY

		Expect(err).NotTo(HaveOccurred())
		Expect(provider).NotTo(BeNil())

		_, ok := provider.(*managermocks.MockGitRepository)
		Expect(ok).To(BeTrue())
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
