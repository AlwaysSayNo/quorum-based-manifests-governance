/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller_test

import (
	"errors"
	// "fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	. "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/controller"
	controllermocks "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/controller/mocks"
	managermocks "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository/mocks"
	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ManifestRequestTemplate Controller", func() {

	const (
		MRTName     = "test-mrt"
		AppName     = "test-app"
		MSRName     = "test-msr"
		MCAName     = "test-mca"
		timeout     = time.Second * 10
		interval    = time.Millisecond * 250
		TestRepoURL = "https://testhub.com/TestUser/test-repo.git"
	)

	var (
		mockCtrl            *gomock.Controller
		mockRepoManager     *controllermocks.MockRepositoryManager
		mockRepo            *managermocks.MockGitRepository
		mockNotifier        *controllermocks.MockNotifier
		governanceNamespace *corev1.Namespace
		argoCDNamespace     *corev1.Namespace
		defaultMRT          governancev1alpha1.ManifestRequestTemplate
		defaultMSR          governancev1alpha1.ManifestSigningRequest
		defaultMCA          governancev1alpha1.ManifestChangeApproval
		defaultApp          argocdv1alpha1.Application
		mrtKey              types.NamespacedName
		defaultInitCommit   string
		defaultRepoChanges  []governancev1alpha1.FileChange
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockRepoManager = controllermocks.NewMockRepositoryManager(mockCtrl)
		mockRepo = managermocks.NewMockGitRepository(mockCtrl)
		mrtReconciler.RepoManager = mockRepoManager
		mrtReconciler.Notifier = mockNotifier

		defaultInitCommit = "abc123def456"
		defaultRepoChanges = []governancev1alpha1.FileChange{
			{Kind: "Deployment", Status: governancev1alpha1.New, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"},
			{Kind: "ManifestRequestTemplate", Status: governancev1alpha1.New, Name: "test-mrt", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/mrt.yaml"},
		}

		// create random governance namespace
		governanceNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "governance-test-ns-",
			},
		}
		Expect(k8sClient.Create(ctx, governanceNamespace)).Should(Succeed())

		// create random application namespace
		argoCDNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "argocd-",
			},
		}
		Expect(k8sClient.Create(ctx, argoCDNamespace)).Should(Succeed())

		// Create default bare minimum MRT
		defaultMRT = governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MRTName,
				Namespace: governanceNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				Version: 1,
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					PublicKey: "FAKE_PGP_KEY",
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      "pgp-secret",
						Namespace: governanceNamespace.Name,
					},
				},
				SSH: &governancev1alpha1.SSHPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      "ssh-secret",
						Namespace: governanceNamespace.Name,
					},
				},
				ArgoCDApplication: governancev1alpha1.ArgoCDApplication{
					Name:      AppName,
					Namespace: argoCDNamespace.Name,
				},
				MSR: governancev1alpha1.ManifestRefOptional{
					Name:      MSRName,
					Namespace: governanceNamespace.Name,
				},
				MCA: governancev1alpha1.ManifestRefOptional{
					Name:      MCAName,
					Namespace: governanceNamespace.Name,
				},
				Governors: governancev1alpha1.GovernorList{
					Members: []governancev1alpha1.Governor{
						{Alias: "$owner", PublicKey: "OWNER_PGP_KEY"},
					},
				},
				Require: governancev1alpha1.ApprovalRule{
					All:    Pointer(true),
					Signer: "$owner",
				},
			},
		}
		mrtKey = types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}

		// Create dependent Application
		defaultApp = argocdv1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: argoCDNamespace.Name},
			Spec: argocdv1alpha1.ApplicationSpec{
				Source: &argocdv1alpha1.ApplicationSource{RepoURL: TestRepoURL, Path: "app-manifests"},
			},
		}

		// Create default bare minimum MSR depending on default MRT
		defaultMSR = governancev1alpha1.ManifestSigningRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultMRT.Spec.MSR.Name,
				Namespace: defaultMRT.Spec.MSR.Namespace,
			},
			Spec: governancev1alpha1.ManifestSigningRequestSpec{
				Version: 0,
				MRT: governancev1alpha1.VersionedManifestRef{
					Name:      mrtKey.Name,
					Namespace: mrtKey.Namespace,
					Version:   defaultMRT.Spec.Version,
				},
				PublicKey: defaultMRT.Spec.PGP.PublicKey,
				GitRepository: governancev1alpha1.GitRepository{
					URL: defaultApp.Spec.Source.RepoURL,
				},
				Location:  *defaultMRT.Spec.Location.DeepCopy(),
				Changes:   defaultRepoChanges,
				Governors: *defaultMRT.Spec.Governors.DeepCopy(),
				Require:   *defaultMRT.Spec.Require.DeepCopy(),
				Status:    governancev1alpha1.Approved,
			},
		}
		defaultMSR.Status.RequestHistory = []governancev1alpha1.ManifestSigningRequestHistoryRecord{
			{
				Version:   defaultMRT.Spec.Version,
				Changes:   defaultMSR.Spec.Changes,
				Governors: defaultMRT.Spec.Governors,
				Require:   defaultMRT.Spec.Require,
				Approves:  defaultMSR.Status.Approves,
				Status:    defaultMSR.Spec.Status,
			},
		}

		// Create default bare minimum MCA depending on default MRT
		defaultMCA = governancev1alpha1.ManifestChangeApproval{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultMRT.Spec.MCA.Name,
				Namespace: defaultMRT.Spec.MCA.Namespace,
			},
			Spec: governancev1alpha1.ManifestChangeApprovalSpec{
				Version: 0,
				MRT: governancev1alpha1.VersionedManifestRef{
					Name:      mrtKey.Name,
					Namespace: mrtKey.Namespace,
					Version:   defaultMRT.Spec.Version,
				},
				MSR: governancev1alpha1.VersionedManifestRef{
					Name:      defaultMSR.Name,
					Namespace: defaultMSR.Namespace,
					Version:   defaultMSR.Spec.Version,
				},
				PublicKey: defaultMRT.Spec.PGP.PublicKey,
				GitRepository: governancev1alpha1.GitRepository{
					URL: defaultApp.Spec.Source.RepoURL,
				},
				LastApprovedCommitSHA: defaultInitCommit,
				Location:              *defaultMRT.Spec.Location.DeepCopy(),
				Changes:               defaultRepoChanges,
				Governors:             *defaultMRT.Spec.Governors.DeepCopy(),
				Require:               *defaultMRT.Spec.Require.DeepCopy(),
			},
		}
		defaultMCA.Status.ApprovalHistory = []governancev1alpha1.ManifestChangeApprovalHistoryRecord{
			{
				CommitSHA: defaultInitCommit,
				Version:   defaultMCA.Spec.Version,
				Changes:   defaultMCA.Spec.Changes,
				Governors: defaultMCA.Spec.Governors,
				Require:   defaultMCA.Spec.Require,
				Approves:  defaultMCA.Status.Approves,
			},
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Context("Reconciliation Lifecycle", func() {
		It("should remove the finalizer when an MRT is deleted", func() {
			// SETUP
			mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}

			// Mock manager and repository calls
			mockLatestRevision := defaultInitCommit
			mockChangedFiles := defaultRepoChanges

			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()

			mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(mockLatestRevision, nil).AnyTimes()
			mockRepo.EXPECT().GetChangedFiles(gomock.Any(), "", mockLatestRevision, gomock.Any()).Return(mockChangedFiles, nil).AnyTimes()

			// Create Application before MRT
			app := &defaultApp
			Expect(k8sClient.Create(ctx, app)).Should(Succeed())

			// Create MRT
			mrt := &defaultMRT
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// ACT + VERIFY
			// Check, that MRT is created with finalized
			Eventually(func() []string {
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
				return updatedMRT.Finalizers
			}, 3, interval).Should(ContainElement(MRTFinalizer))

			// Delete created MSR
			By("Deleting the initialized MRT to test finalization")
			Expect(k8sClient.Delete(ctx, mrt)).Should(Succeed())

			// Verify, that finalized is removed
			Eventually(func() bool {
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				err := k8sClient.Get(ctx, mrtKey, updatedMRT)
				return apierrors.IsNotFound(err)
			}, 3, interval).Should(BeTrue(), "The MRT should be fully deleted after the finalizer is removed")
		})

		It("should fail initialization if the referenced Argo CD Application does not exist", func() {
			// SETUP

			// Don't setup the Application

			mrt := &defaultMRT
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// ACT + ASSERT
			// Expect: Reconcile should fail when enters onMRTCreation, while getApplication returns a "not found" error.
			// Controller will keep retrying. In the end, assert that the MRTFinalize isn't added
			Consistently(func() []string {
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}, updatedMRT); err != nil {
					// Return nil so Consistently doesn't fail on IsNotFound error
					return nil
				}
				return updatedMRT.Finalizers
			}, "2s", interval).Should(BeEmpty(), "The finalizer should never be added if the dependent Application is missing")
		})

		// TODO: somehow it breaks some tests
		// It("should initialize an MRT by adding a finalizer and creating default MSR and MCA", func() {
		// 	// SETUP
		// 	// Mock manager and repository calls
		// 	mockLatestRevision := "abc123def456"
		// 	mockChangedFiles := []governancev1alpha1.FileChange{
		// 		{Kind: "Deployment", Status: governancev1alpha1.New, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"},
		// 	}

		// 	// Without AnyTimes
		// 	mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(mockLatestRevision, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetChangedFiles(gomock.Any(), "", mockLatestRevision, "app-manifests").Return(mockChangedFiles, nil).AnyTimes()

		// 	// Create Application before MRT
		// 	app := &defaultApp
		// 	Expect(k8sClient.Create(ctx, app)).Should(Succeed())

		// 	// Create MRT
		// 	mrt := &defaultMRT
		// 	Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

		// 	// ACT + ASSERT
		// 	// Check MRT exists
		// 	By("ensuring the finalizer is added")
		// 	Eventually(func() []string {
		// 		updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
		// 		_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
		// 		return updatedMRT.Finalizers
		// 	}, timeout, interval).Should(ContainElement(MRTFinalizer))

		// 	// Check MSR exists
		// 	By("ensuring the default MSR is created")
		// 	createdMSR := &governancev1alpha1.ManifestSigningRequest{}
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, types.NamespacedName{Name: MSRName, Namespace: governanceNamespace.Name}, createdMSR)
		// 	}, timeout, interval).Should(Succeed())
		// 	Expect(createdMSR.Spec.Changes).To(HaveLen(1))
		// 	Expect(createdMSR.Spec.Changes[0].Name).To(Equal("my-app"))
		// 	Expect(createdMSR.OwnerReferences).To(HaveLen(1), "MSR should be owned by the MRT")
		// 	Expect(createdMSR.OwnerReferences[0].Name).To(Equal(MRTName))
		// 	Expect(createdMSR.Spec.Version).To(Equal(0)) // expect version 0
		// 	Expect(createdMSR.Spec.MRT.Name).To(Equal(MRTName))
		// 	Expect(createdMSR.Spec.MRT.Namespace).To(Equal(governanceNamespace.Name))
		// 	Expect(createdMSR.Spec.MRT.Version).To(Equal(defaultMRT.Spec.Version))
		// 	Expect(createdMSR.Spec.PublicKey).To(Equal(defaultMRT.Spec.PGP.PublicKey))
		// 	Expect(createdMSR.Spec.GitRepository.URL).To(Equal(TestRepoURL))
		// 	Expect(createdMSR.Spec.Status).To(Equal(governancev1alpha1.Approved))
		// 	Expect(createdMSR.Spec.Governors).To(Equal(defaultMRT.Spec.Governors))
		// 	Expect(createdMSR.Spec.Require).To(Equal(defaultMRT.Spec.Require))
		// 	Expect(createdMSR.Status.RequestHistory).To(HaveLen(1))

		// 	// Check MCA exists
		// 	By("ensuring the default MCA is created")
		// 	createdMCA := &governancev1alpha1.ManifestChangeApproval{}
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, types.NamespacedName{Name: MCAName, Namespace: governanceNamespace.Name}, createdMCA)
		// 	}, timeout, interval).Should(Succeed())
		// 	By(fmt.Sprintf("%#v\n", createdMCA.Spec))
		// 	By(fmt.Sprintf("%#v\n", createdMCA.Status))
		// 	Expect(createdMCA.Status.LastApprovedCommitSHA).To(Equal(mockLatestRevision))
		// 	Expect(createdMCA.OwnerReferences).To(HaveLen(1), "MCA should be owned by the MRT")
		// 	Expect(createdMCA.Spec.MRT).To(Equal(createdMSR.Spec.MRT))
		// 	Expect(createdMCA.Spec.MSR.Name).To(Equal(MSRName))
		// 	Expect(createdMCA.Spec.MSR.Namespace).To(Equal(governanceNamespace.Name))
		// 	Expect(createdMCA.Spec.MSR.Version).To(Equal(0))
		// 	Expect(createdMCA.Spec.PublicKey).To(Equal(defaultMRT.Spec.PGP.PublicKey))
		// 	Expect(createdMCA.Spec.GitRepository.URL).To(Equal(TestRepoURL))
		// 	Expect(createdMCA.Spec.Governors).To(Equal(defaultMRT.Spec.Governors))
		// 	Expect(createdMCA.Spec.Require).To(Equal(defaultMRT.Spec.Require))
		// 	Expect(createdMCA.Spec.Changes).To(Equal(createdMSR.Spec.Changes))
		// 	Expect(createdMCA.Status.ApprovalHistory).To(HaveLen(1))

		// 	// Check MRT is updated
		// 	By("ensuring the MRT status is updated")
		// 	updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
		// 	Expect(k8sClient.Get(ctx, mrtKey, updatedMRT)).To(Succeed())
		// 	Expect(updatedMRT.Status.LastObservedCommitHash).To(Equal(mockLatestRevision))
		// 	Expect(updatedMRT.Status.LastMSRVersion).To(Equal(0)) // expect version 0
		// })

		It("should fail reconciliation if the repository provider cannot be initialized", func() {
			// SETUP
			// Create all dependent resources
			Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())

			// Setup manager mock to fail
			mockRepoManager.EXPECT().
				GetProviderForMRT(gomock.Any(), gomock.Any()).
				Return(nil, errors.New("unknown repository type")).
				AnyTimes()

			// Create the MRT with finalizer
			mrt := &defaultMRT
			mrt.Finalizers = []string{MRTFinalizer}
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// Wait, until first reconcile finished
			Eventually(func() error {
				return k8sClient.Get(ctx, mrtKey, mrt)
			}, timeout, interval).Should(Succeed())

			// // Update revision to trigger revision reconciliation
			mrt.Status.RevisionsQueue = []string{"abc456def"}
			Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

			// ACT + ASSERT
			By("checking that the revision queue is not popped")
			// Use Consistently to prove that over a period of time, the queue length remains 1.
			// Reconcile loop will keep failing and revision won be popped.
			Consistently(func() int {
				checkMRT := &governancev1alpha1.ManifestRequestTemplate{}
				if err := k8sClient.Get(ctx, mrtKey, checkMRT); err != nil {
					return -1 // Return an invalid length on error
				}
				return len(checkMRT.Status.RevisionsQueue)
			}, 2, interval).Should(Equal(1), "The revision queue should not be popped when repository initialization fails")
		})

		It("should do nothing and pop the revision if it's the latest in MCA history", func() {
			// SETUP
			// Create MRT linked resources
			Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())

			// Setup mockRepoManager not to fail
			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()

			// Create an MRT with the finalizer and the same revision in queue as default MCA
			mrt := &defaultMRT
			initialCommit := defaultInitCommit
			mrt.Finalizers = []string{MRTFinalizer}
			mrt.Status.RevisionsQueue = []string{initialCommit}
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())
			Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

			// ACT + ASSERT
			// Expect: reconciler sees revision and latest MCA, popped revision and does nothing.
			Eventually(func() []string {
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
				return updatedMRT.Status.RevisionsQueue
			}, 3, interval).Should(BeEmpty())
		})

		It("should do nothing and pop the revision if it corresponds to an old MCA entry", func() {
			// SETUP
			// Create MRT linked resources
			Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())

			// Setup mockRepoManager not to fail
			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()

			// Setup MCA with 2 records in approvalHistory
			oldCommit := "111111"
			latestCommit := "222222"

			mca := &defaultMCA
			mca.Status.ApprovalHistory = []governancev1alpha1.ManifestChangeApprovalHistoryRecord{
				{
					CommitSHA: oldCommit,
					Version:   0,
					Changes:   defaultMCA.Spec.Changes,
					Governors: defaultMCA.Spec.Governors,
					Require:   defaultMCA.Spec.Require,
					Approves:  defaultMCA.Status.Approves,
				},
				{
					CommitSHA: latestCommit,
					Version:   1,
					Changes: []governancev1alpha1.FileChange{
						{Kind: "Deployment", Status: governancev1alpha1.Updated, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"},
					},
					Governors: defaultMCA.Spec.Governors,
					Require:   defaultMCA.Spec.Require,
					Approves:  defaultMCA.Status.Approves,
				},
			}
			mca.Spec.LastApprovedCommitSHA = latestCommit
			Expect(k8sClient.Create(ctx, mca)).Should(Succeed())
			Expect(k8sClient.Status().Update(ctx, mca)).Should(Succeed())

			// Create an MRT with the finalizer and the old revision in the queue
			mrt := &defaultMRT
			mrt.Finalizers = []string{MRTFinalizer}
			mrt.Status.RevisionsQueue = []string{oldCommit}
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// ACT & ASSERT
			// Expect: reconciler sees revision and old MCA, popped revision and does nothing.
			Eventually(func() []string {
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
				return updatedMRT.Status.RevisionsQueue
			}, timeout, interval).Should(BeEmpty())
		})

		It("should requeue with an error if HasRevision fails", func() {
			// SETUP
			mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}
			revisionToTest := "abc456def"

			// Create dependencies
			Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())

			// Set up mock to fail on HasRevision
			expectedErr := errors.New("git command failed")
			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
			mockRepo.EXPECT().HasRevision(gomock.Any(), revisionToTest).Return(false, expectedErr).AnyTimes()

			// Create the MRT with a finalizer and an item in the queue
			mrt := &defaultMRT
			mrt.Finalizers = []string{MRTFinalizer}
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// Wait, until first reconcile finished
			Eventually(func() error {
				return k8sClient.Get(ctx, mrtKey, mrt)
			}, timeout, interval).Should(Succeed())

			mrt.Status.RevisionsQueue = []string{revisionToTest}
			Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

			// ACT + ASSERT
			// We expect the reconcile to fail and for the queue to NOT be popped.
			Consistently(func() int {
				checkMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, checkMRT)
				return len(checkMRT.Status.RevisionsQueue)
			}, 3, interval).Should(Equal(1), "The queue should not be popped if HasRevision fails")
		})

		It("should return a terminal error if HasRevision returns false", func() {
			// SETUP
			mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}
			revisionToTest := "abc456def" // A commit that the mock will say doesn't exist

			Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())

			// Set up mock to return false for HasRevision
			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
			mockRepo.EXPECT().HasRevision(gomock.Any(), revisionToTest).Return(false, nil).AnyTimes()

			mrt := &defaultMRT
			mrt.Finalizers = []string{MRTFinalizer}
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// Wait, until first reconcile finished
			Eventually(func() error {
				return k8sClient.Get(ctx, mrtKey, mrt)
			}, timeout, interval).Should(Succeed())

			mrt.Status.RevisionsQueue = []string{revisionToTest}
			Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

			// ACT + ASSERT
			// The reconcile should fail with a non-requeueable error. The item will not be popped.
			Consistently(func() int {
				checkMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, checkMRT)
				return len(checkMRT.Status.RevisionsQueue)
			}, 2, interval).Should(Equal(1))
		})

		It("should requeue with an error if GetLatestRevision fails", func() {
			// SETUP
			revisionToTest := "abc456def"

			Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())

			// Set up mocks: HasRevision succeeds, but GetLatestRevision fails
			expectedErr := errors.New("failed to get latest")
			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
			mockRepo.EXPECT().HasRevision(gomock.Any(), revisionToTest).Return(true, nil).AnyTimes()
			mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return("", expectedErr).AnyTimes()

			mrt := &defaultMRT
			mrt.Finalizers = []string{MRTFinalizer}
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// Wait, until first reconcile finished
			Eventually(func() error {
				return k8sClient.Get(ctx, mrtKey, mrt)
			}, timeout, interval).Should(Succeed())

			mrt.Status.RevisionsQueue = []string{revisionToTest}
			Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

			// ACT & ASSERT
			// The reconcile should fail. The item will not be popped.
			Consistently(func() int {
				checkMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, checkMRT)
				return len(checkMRT.Status.RevisionsQueue)
			}, 2, interval).Should(Equal(1))
		})

		// It("should return an error if a newer revision is found that already has an MCA history entry", func() {
		// 	// SETUP
		// 	mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}
		// 	currentRevisionInQueue := "111" // The revision is being processed
		// 	newerLatestRevision := "222"    // The latest revision in repo

		// 	// Create MCA that already inconsistently contains the "newer" revision
		// 	mca := &defaultMCA
		// 	Expect(k8sClient.Create(ctx, mca)).Should(Succeed())

		// 	// Wait, until first reconcile finished
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, types.NamespacedName{Name: defaultMCA.Name, Namespace: defaultMCA.Namespace}, mca)
		// 	}, 2, interval).Should(Succeed())

		// 	mca.Status.ApprovalHistory = []governancev1alpha1.ManifestChangeApprovalHistoryRecord{
		// 		{
		// 			CommitSHA: newerLatestRevision,
		// 			Version:   1,
		// 			Changes: []governancev1alpha1.FileChange{
		// 				{Kind: "Deployment", Status: governancev1alpha1.Updated, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"},
		// 			},
		// 			Governors: defaultMCA.Spec.Governors,
		// 			Require:   defaultMCA.Spec.Require,
		// 			Approves:  defaultMCA.Status.Approves,
		// 		},
		// 	}
		// 	Expect(k8sClient.Status().Update(ctx, mca)).Should(Succeed())

		// 	Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
		// 	Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())

		// 	// Set up mocks to create this scenario
		// 	mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
		// 	mockRepo.EXPECT().HasRevision(gomock.Any(), currentRevisionInQueue).Return(true, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(newerLatestRevision, nil).AnyTimes()

		// 	mrt := &defaultMRT
		// 	mrt.Finalizers = []string{MRTFinalizer}
		// 	Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

		// 	// Wait, until first reconcile finished
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, mrtKey, mrt)
		// 	}, 2, interval).Should(Succeed())

		// 	mrt.Status.RevisionsQueue = []string{currentRevisionInQueue}
		// 	Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

		// 	// ACT + ASSERT
		// 	// The reconcile should fail with error. The queue is not popped.
		// 	Consistently(func() int {
		// 		checkMRT := &governancev1alpha1.ManifestRequestTemplate{}
		// 		_ = k8sClient.Get(ctx, mrtKey, checkMRT)
		// 		return len(checkMRT.Status.RevisionsQueue)
		// 	}, 2, interval).Should(Equal(1))
		// })

		// It("should add a newer revision to the queue and pop the current one", func() {
		// 	// SETUP
		// 	mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}
		// 	currentRevisionInQueue := "111"
		// 	newerLatestRevision := "222"

		// 	// Create an MCA with an empty history
		// 	Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())
		// 	Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())

		// 	// Set up mocks
		// 	mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
		// 	mockRepo.EXPECT().HasRevision(gomock.Any(), currentRevisionInQueue).Return(true, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(newerLatestRevision, nil).AnyTimes()

		// 	mrt := &defaultMRT
		// 	mrt.Finalizers = []string{MRTFinalizer}
		// 	Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

		// 	// Wait, until first reconcile finished
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, mrtKey, mrt)
		// 	}, timeout, interval).Should(Succeed())

		// 	mrt.Status.RevisionsQueue = []string{currentRevisionInQueue}
		// 	Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

		// 	// ACT + ASSERT
		// 	// Expect: controller pops "111" and add "222" to the queue.
		// 	Eventually(func() []string {
		// 		updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
		// 		_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
		// 		return updatedMRT.Status.RevisionsQueue
		// 	}, timeout, interval).Should(ConsistOf(newerLatestRevision))
		// })

		// It("should skip MSR creation and pop the queue if no relevant file changes are found", func() {
		// 	// ARRANGE
		// 	revisionToTest := "abc456def"

		// 	// Create all dependencies
		// 	Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
		// 	Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())
		// 	Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
		// 	// Set up mocks. GetChangedFiles returns a non-manifest file that will be filtered out.
		// 	mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
		// 	mockRepo.EXPECT().HasRevision(gomock.Any(), revisionToTest).Return(true, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(revisionToTest, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetChangedFiles(gomock.Any(), gomock.Any(), revisionToTest, gomock.Any()).
		// 		Return([]governancev1alpha1.FileChange{{Path: "app-manifests/README.md"}}, nil).AnyTimes()

		// 	// Create the MRT with the revision in its queue
		// 	mrt := &defaultMRT
		// 	mrt.Finalizers = []string{MRTFinalizer}
		// 	Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

		// 	// Wait, until first reconcile finished
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, mrtKey, mrt)
		// 	}, timeout, interval).Should(Succeed())

		// 	mrt.Status.RevisionsQueue = []string{revisionToTest}
		// 	Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

		// 	// ACT & ASSERT
		// 	// We expect the controller to run startMSRProcess, see that there are no relevant
		// 	// changes, and pop the queue without creating a new MSR.
		// 	// The LastObservedCommitHash should be updated to the processed revision.
		// 	Eventually(func() *governancev1alpha1.ManifestRequestTemplateStatus {
		// 		updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
		// 		_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
		// 		return &updatedMRT.Status
		// 	}, timeout, interval).Should(And(
		// 		WithTransform(func(s *governancev1alpha1.ManifestRequestTemplateStatus) []string { return s.RevisionsQueue }, BeEmpty()),
		// 		WithTransform(func(s *governancev1alpha1.ManifestRequestTemplateStatus) string { return s.LastObservedCommitHash }, Equal(revisionToTest)),
		// 	))
		// })

		// It("should successfully create and push an MSR when relevant changes are found", func() {
		// 	// ARRANGE
		// 	mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}
		// 	revisionToTest := "abc456def"
		// 	msrPushCommit := "msr456commit"

		// 	// Create all dependencies
		// 	Expect(k8sClient.Create(ctx, &defaultApp)).Should(Succeed())
		// 	Expect(k8sClient.Create(ctx, &defaultMCA)).Should(Succeed())

		// 	// Create the initial MSR with version 0
		// 	Expect(k8sClient.Create(ctx, &defaultMSR)).Should(Succeed())
		// 	Expect(k8sClient.Status().Update(ctx, &defaultMSR)).Should(Succeed())

		// 	// Set up mocks for the full flow
		// 	mockChangedFiles := []governancev1alpha1.FileChange{{Kind: "Deployment", Status: governancev1alpha1.Updated, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"}}
		// 	mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
		// 	mockRepo.EXPECT().HasRevision(gomock.Any(), revisionToTest).Return(true, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(revisionToTest, nil).AnyTimes()
		// 	mockRepo.EXPECT().GetChangedFiles(gomock.Any(), gomock.Any(), revisionToTest, gomock.Any()).Return(mockChangedFiles, nil).AnyTimes()
		// 	mockRepo.EXPECT().PushMSR(gomock.Any(), gomock.Any()).Return(msrPushCommit, nil).AnyTimes()

		// 	// Set up notifier mock
		// 	mockNotifier.EXPECT().NotifyGovernors(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		// 	// Create the MRT with the revision in its queue
		// 	mrt := &defaultMRT
		// 	mrt.Finalizers = []string{MRTFinalizer}
		// 	Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

		// 	// Wait, until first reconcile finished
		// 	Eventually(func() error {
		// 		return k8sClient.Get(ctx, mrtKey, mrt)
		// 	}, timeout, interval).Should(Succeed())

		// 	mrt.Status.RevisionsQueue = []string{revisionToTest}
		// 	Expect(k8sClient.Status().Update(ctx, mrt)).Should(Succeed())

		// 	// ACT + ASSERT
		// 	// We wait for the final state of the MRT status
		// 	By("verifying the MRT status is updated correctly after processing")
		// 	Eventually(func() *governancev1alpha1.ManifestRequestTemplateStatus {
		// 		updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
		// 		_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
		// 		return &updatedMRT.Status
		// 	}, timeout, interval).Should(And(
		// 		// The queue should be popped
		// 		WithTransform(func(s *governancev1alpha1.ManifestRequestTemplateStatus) []string { return s.RevisionsQueue }, BeEmpty()),
		// 		// The LastObservedCommitHash should be the NEW commit hash from pushing the MSR
		// 		WithTransform(func(s *governancev1alpha1.ManifestRequestTemplateStatus) string { return s.LastObservedCommitHash }, Equal(msrPushCommit)),
		// 	))

		// 	By("verifying the MSR object was updated in the cluster")
		// 	updatedMSR := &governancev1alpha1.ManifestSigningRequest{}
		// 	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: MSRName, Namespace: governanceNamespace.Name}, updatedMSR)).Should(Succeed())
		// 	Expect(updatedMSR.Spec.Version).To(Equal(1))
		// 	Expect(updatedMSR.Spec.Changes).To(HaveLen(1))
		// 	Expect(updatedMSR.Status.RequestHistory).To(HaveLen(1))
		// })
	})

})
