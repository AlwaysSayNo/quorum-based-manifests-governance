package repository_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"

	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
	managermocks "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository/mocks"
)

var _ = Describe("Repository Manager", func() {

	const (
		MRTName       = "test-mrt"
		SSHSecretName = "test-git-creds"
		PGPSecretName = "test-pgp-cred-name"
		TestRepoURL   = "git@testhub.com:TestUser/test-repo.git"
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

		// Create known_hosts file
		knownHostsDir := GinkgoT().TempDir()
		knownHostsPath := filepath.Join(knownHostsDir, "known_hosts")
		err := os.WriteFile(knownHostsPath, []byte(""), 0644)
		Expect(err).NotTo(HaveOccurred())

		repoManager = manager.NewManager(k8sClient, GinkgoT().TempDir(), knownHostsPath)
	})

	AfterEach(func() {
		// Clean up by deleting the namespace. Automatically garbage-collects all objects created inside it
		Expect(k8sClient.Delete(ctx, testNamespace)).Should(Succeed())
	})

	Describe("GetProviderForMRT Method", func() {
		Context("when validating MRT configuration", func() {
			It("should fail if RepoURL is empty", func() {
				// SETUP
				// Create a dummy MRT resource
				mrt := &governancev1alpha1.ManifestRequestTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      MRTName,
						Namespace: testNamespace.Name,
					},

					Spec: governancev1alpha1.ManifestRequestTemplateSpec{
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{URL: ""},
						},
					},
				}

				// ACT
				provider, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY

				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("repository URL is not defined in MRT spec"))
				Expect(provider).Should(BeNil())
			})
		})

		Context("when no providers are registered", func() {
			It("should fail if provider list is empty", func() {
				// SETUP

				// Create MRT
				mrt := &governancev1alpha1.ManifestRequestTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      MRTName,
						Namespace: testNamespace.Name,
					},
					Spec: governancev1alpha1.ManifestRequestTemplateSpec{
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{URL: TestRepoURL},
						},
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
		})

		Context("when PGP secrets are invalid", func() {
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
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{URL: TestRepoURL},
						},
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
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{URL: TestRepoURL},
						},
					},
				}

				// ACT

				provider, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY

				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to fetch pgp secret"))
				Expect(provider).Should(BeNil())
			})
		})

		Context("when SSH secrets are invalid", func() {
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
						PGP: &governancev1alpha1.PGPPrivateKeySecret{
							SecretsRef: governancev1alpha1.ManifestRef{
								Name:      PGPSecretName,
								Namespace: testNamespace.Name,
							},
							PublicKey: "FAKE_PGP_KEY",
						},
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{
								URL: TestRepoURL,
								SecretsRef: &governancev1alpha1.ManifestRef{
									Name:      SSHSecretName,
									Namespace: testNamespace.Name,
								},
							},
						},
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
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{
								URL: TestRepoURL,
							},
						},
					},
				}

				// ACT

				provider, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY

				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ssh information is nil"))
				Expect(provider).Should(BeNil())
			})
		})

		Context("when provider does not support the repository URL", func() {
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
						"ssh-privateKey": privateKey,
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
						PGP: &governancev1alpha1.PGPPrivateKeySecret{
							SecretsRef: governancev1alpha1.ManifestRef{
								Name:      PGPSecretName,
								Namespace: testNamespace.Name,
							},
							PublicKey: "FAKE_PGP_KEY",
						},
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{
								URL: TestRepoURL,
								SecretsRef: &governancev1alpha1.ManifestRef{
									Name:      SSHSecretName,
									Namespace: testNamespace.Name,
								},
							},
						},
					},
				}

				// Inject dummy provider that doesn't support the URL
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
		})

		Context("when all configurations are valid", func() {
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
						"ssh-privateKey": privateKey,
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
						GitRepository: governancev1alpha1.GitRepository{
							SSH: governancev1alpha1.GitSSH{
								URL: TestRepoURL,
								SecretsRef: &governancev1alpha1.ManifestRef{
									Name:      SSHSecretName,
									Namespace: testNamespace.Name,
								},
							},
						},
						PGP: &governancev1alpha1.PGPPrivateKeySecret{
							SecretsRef: governancev1alpha1.ManifestRef{
								Name:      PGPSecretName,
								Namespace: testNamespace.Name,
							},
							PublicKey: "FAKE_PGP_KEY",
						},
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
	})

	Describe("Secret Refresh Mechanism", func() {
		var (
			sshSecret *corev1.Secret
			pgpSecret *corev1.Secret
			mrt       *governancev1alpha1.ManifestRequestTemplate
		)

		BeforeEach(func() {
			// Create SSH secret
			privateKey, _, err := generateTestSSHKey()
			Expect(err).NotTo(HaveOccurred())
			sshSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SSHSecretName,
					Namespace: testNamespace.Name,
				},
				StringData: map[string]string{
					"ssh-privateKey": privateKey,
					"passphrase":     "",
				},
			}
			Expect(k8sClient.Create(ctx, sshSecret)).Should(Succeed())

			// Create PGP secret
			pgpSecret = &corev1.Secret{
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
			mrt = &governancev1alpha1.ManifestRequestTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      MRTName,
					Namespace: testNamespace.Name,
				},
				Spec: governancev1alpha1.ManifestRequestTemplateSpec{
					GitRepository: governancev1alpha1.GitRepository{
						SSH: governancev1alpha1.GitSSH{
							URL: TestRepoURL,
							SecretsRef: &governancev1alpha1.ManifestRef{
								Name:      SSHSecretName,
								Namespace: testNamespace.Name,
							},
						},
					},
					PGP: &governancev1alpha1.PGPPrivateKeySecret{
						SecretsRef: governancev1alpha1.ManifestRef{
							Name:      PGPSecretName,
							Namespace: testNamespace.Name,
						},
						PublicKey: "FAKE_PGP_KEY",
					},
				},
			}

			// Inject dummy provider
			repoManager.Register(mockRepoFactory)
		})

		Context("when TTL has not expired", func() {
			It("should not refresh secrets and return cached provider", func() {
				// SETUP
				// Set a long TTL (1 hour)
				repoManager.SetSecretRefreshTTL(3600)

				// First call to create and cache the provider
				provider1, err := repoManager.GetProviderForMRT(ctx, mrt)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider1).NotTo(BeNil())

				// ACT
				// Second call immediately after - should use cached provider
				provider2, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY
				Expect(err).NotTo(HaveOccurred())
				Expect(provider2).NotTo(BeNil())
				Expect(provider2).To(Equal(provider1), "should return the same cached provider instance")
			})
		})

		Context("when TTL has expired and secrets are unchanged", func() {
			It("should successfully return provider after refresh attempt", func() {
				// SETUP
				// Set a very short TTL (1 second)
				repoManager.SetSecretRefreshTTL(1)

				// First call to create and cache the provider
				provider1, err := repoManager.GetProviderForMRT(ctx, mrt)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider1).NotTo(BeNil())

				// Wait for TTL to expire
				time.Sleep(2 * time.Second)

				// ACT
				// Second call after TTL expiry - should attempt refresh
				provider2, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY
				Expect(err).NotTo(HaveOccurred())
				Expect(provider2).NotTo(BeNil())
				Expect(provider2).To(Equal(provider1), "should return the same provider instance")
			})
		})

		Context("when TTL has expired and secrets are deleted", func() {
			It("should still return cached provider when PGP secret is missing", func() {
				// SETUP
				// Set a very short TTL (1 second)
				repoManager.SetSecretRefreshTTL(1)

				// First call to create and cache the provider
				provider1, err := repoManager.GetProviderForMRT(ctx, mrt)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider1).NotTo(BeNil())

				// Wait for TTL to expire
				time.Sleep(2 * time.Second)

				// Delete PGP secret
				Expect(k8sClient.Delete(ctx, pgpSecret)).Should(Succeed())

				// ACT
				// Second call after TTL expiry and secret deletion
				provider2, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY
				// Should still return the cached provider (refresh fails silently in background)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider2).NotTo(BeNil())
				Expect(provider2).To(Equal(provider1), "should return the same cached provider")
			})

			It("should still return cached provider when SSH secret is missing", func() {
				// SETUP
				// Set a very short TTL (1 second)
				repoManager.SetSecretRefreshTTL(1)

				// First call to create and cache the provider
				provider1, err := repoManager.GetProviderForMRT(ctx, mrt)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider1).NotTo(BeNil())

				// Wait for TTL to expire
				time.Sleep(2 * time.Second)

				// Delete SSH secret
				Expect(k8sClient.Delete(ctx, sshSecret)).Should(Succeed())

				// ACT
				// Second call after TTL expiry and secret deletion
				provider2, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY
				// Should still return the cached provider (refresh fails silently in background)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider2).NotTo(BeNil())
				Expect(provider2).To(Equal(provider1), "should return the same cached provider")
			})
		})

		Context("when TTL has expired and secrets are updated", func() {
			It("should successfully return provider after credential update", func() {
				// SETUP
				// Set a very short TTL (1 second)
				repoManager.SetSecretRefreshTTL(1)

				// First call to create and cache the provider
				provider1, err := repoManager.GetProviderForMRT(ctx, mrt)
				Expect(err).NotTo(HaveOccurred())
				Expect(provider1).NotTo(BeNil())

				// Wait for TTL to expire
				time.Sleep(2 * time.Second)

				// Get fresh secrets to update
				freshSSHSecret := &corev1.Secret{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: SSHSecretName, Namespace: testNamespace.Name}, freshSSHSecret)).Should(Succeed())

				freshPGPSecret := &corev1.Secret{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: PGPSecretName, Namespace: testNamespace.Name}, freshPGPSecret)).Should(Succeed())

				// Update secrets with new values
				privateKeyNew, _, err := generateTestSSHKey()
				Expect(err).NotTo(HaveOccurred())
				freshSSHSecret.StringData = map[string]string{
					"ssh-privateKey": privateKeyNew,
					"passphrase":     "new-passphrase",
				}
				Expect(k8sClient.Update(ctx, freshSSHSecret)).Should(Succeed())

				freshPGPSecret.StringData = map[string]string{
					"privateKey": "NEW_FAKE_PGP_KEY",
					"passphrase": "NEW_FAKE_PASSPHRASE",
				}
				Expect(k8sClient.Update(ctx, freshPGPSecret)).Should(Succeed())

				// ACT
				// Second call after TTL expiry and secret updates
				provider2, err := repoManager.GetProviderForMRT(ctx, mrt)

				// VERIFY
				Expect(err).NotTo(HaveOccurred())
				Expect(provider2).NotTo(BeNil())
				Expect(provider2).To(Equal(provider1), "should return the same provider instance")
			})
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
