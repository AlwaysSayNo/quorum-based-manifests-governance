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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"
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
		QueueName   = "queue-test-mrt"
		timeout     = time.Second * 10
		interval    = time.Millisecond * 250
		TestRepoURL = "git@testhub.com:TestUser/test-repo.git"
	)

	var (
		mockCtrl            *gomock.Controller
		mockRepoManager     *controllermocks.MockRepositoryManager
		mockRepo            *managermocks.MockGitRepository
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
				GitRepository: governancev1alpha1.GitRepository{
					SSH: governancev1alpha1.GitSSH{
						URL: TestRepoURL,
						SecretsRef: &governancev1alpha1.ManifestRef{
							Name:      "ssh-secret",
							Namespace: governanceNamespace.Name,
						},
					},
				},
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					PublicKey: "FAKE_PGP_KEY",
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      "pgp-secret",
						Namespace: governanceNamespace.Name,
					},
				},
				ArgoCD: governancev1alpha1.ArgoCD{
					Application: governancev1alpha1.ManifestRef{
						Name:      AppName,
						Namespace: argoCDNamespace.Name,
					},
				},
				MSR: governancev1alpha1.ManifestRef{
					Name:      MSRName,
					Namespace: governanceNamespace.Name,
				},
				MCA: governancev1alpha1.ManifestRef{
					Name:      MCAName,
					Namespace: governanceNamespace.Name,
				},
				GovernanceFolderPath: ".qubmango",
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
				Source: &argocdv1alpha1.ApplicationSource{
					RepoURL:        TestRepoURL,
					Path:           "app-manifests",
					TargetRevision: "main",
				},
			},
		}

		// Create default bare minimum MSR depending on default MRT
		defaultMSR = governancev1alpha1.ManifestSigningRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultMRT.Spec.MSR.Name,
				Namespace: defaultMRT.Spec.MSR.Namespace,
			},
			Spec: governancev1alpha1.ManifestSigningRequestSpec{
				Version:           0,
				CommitSHA:         defaultInitCommit,
				PreviousCommitSHA: "",
				MRT: governancev1alpha1.VersionedManifestRef{
					Name:      mrtKey.Name,
					Namespace: mrtKey.Namespace,
					Version:   defaultMRT.Spec.Version,
				},
				PublicKey:        defaultMRT.Spec.PGP.PublicKey,
				GitRepositoryURL: defaultApp.Spec.Source.RepoURL,
				Locations: governancev1alpha1.Locations{
					GovernancePath: ".qubmango",
					SourcePath:     "app-manifests",
				},
				Changes:   defaultRepoChanges,
				Governors: *defaultMRT.Spec.Governors.DeepCopy(),
				Require:   *defaultMRT.Spec.Require.DeepCopy(),
			},
		}
		defaultMSR.Status.RequestHistory = []governancev1alpha1.ManifestSigningRequestHistoryRecord{
			{
				CommitSHA:         defaultInitCommit,
				PreviousCommitSHA: "",
				Version:           defaultMRT.Spec.Version,
				Changes:           defaultMSR.Spec.Changes,
				Governors:         defaultMRT.Spec.Governors,
				Require:           defaultMRT.Spec.Require,
				Status:            governancev1alpha1.Approved,
			},
		}

		// Create default bare minimum MCA depending on default MRT
		defaultMCA = governancev1alpha1.ManifestChangeApproval{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultMRT.Spec.MCA.Name,
				Namespace: defaultMRT.Spec.MCA.Namespace,
			},
			Spec: governancev1alpha1.ManifestChangeApprovalSpec{
				Version:           0,
				CommitSHA:         defaultInitCommit,
				PreviousCommitSHA: "",
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
				PublicKey:        defaultMRT.Spec.PGP.PublicKey,
				GitRepositoryURL: defaultApp.Spec.Source.RepoURL,

				Locations: governancev1alpha1.Locations{
					GovernancePath: ".qubmango",
					SourcePath:     "app-manifests",
				},
				Changes:             defaultRepoChanges,
				Governors:           *defaultMRT.Spec.Governors.DeepCopy(),
				Require:             *defaultMRT.Spec.Require.DeepCopy(),
				CollectedSignatures: []governancev1alpha1.Signature{},
			},
		}
		defaultMCA.Status.ApprovalHistory = []governancev1alpha1.ManifestChangeApprovalHistoryRecord{
			{
				CommitSHA:         defaultInitCommit,
				PreviousCommitSHA: "",
				Version:           defaultMCA.Spec.Version,
				Changes:           defaultMCA.Spec.Changes,
				Governors:         defaultMCA.Spec.Governors,
				Require:           defaultMCA.Spec.Require,
			},
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Context("reconcileCreate - Initialization Flow", func() {
		Describe("Negative Scenario: ArgoCD Application does not exist", func() {
			It("should fail initialization and not add finalizer when Application is missing", func() {
				// SETUP: Don't create the Application, only create MRT
				// Mock GetProviderForMRT since controller calls it even when Application is missing
				mockRepoManager.EXPECT().
					GetProviderForMRT(gomock.Any(), gomock.Any()).
					Return(mockRepo, nil).
					AnyTimes()

				mrt := defaultMRT.DeepCopy()
				Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

				// Wait a moment for the controller to attempt reconciliation
				time.Sleep(500 * time.Millisecond)

				// VERIFY: MRT should NOT have finalizer added
				Eventually(func() []string {
					updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
					_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
					return updatedMRT.Finalizers
				}, timeout, interval).Should(BeEmpty(), "Finalizer should not be added when Application is missing")

				// ActionState should remain empty or stuck at an initialization state
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				Expect(k8sClient.Get(ctx, mrtKey, updatedMRT)).To(Succeed())
				Expect(updatedMRT.Status.ActionState).To(Or(
					Equal(governancev1alpha1.MRTActionStateEmpty),
					Equal(governancev1alpha1.MRTActionStateSaveArgoCDTargetRevision),
				))
			})
		})

		Describe("Negative Scenario: Governance path is not empty", func() {
			It("should fail initialization when governance folder contains files", func() {
				// SETUP: Create Application
				app := defaultApp.DeepCopy()
				Expect(k8sClient.Create(ctx, app)).Should(Succeed())

				// Mock repository to simulate non-empty governance folder
				mockRepoManager.EXPECT().
					GetProviderForMRT(gomock.Any(), gomock.Any()).
					Return(mockRepo, nil).
					AnyTimes()

				// The controller will call GetLatestRevision for SaveArgoCDTargetRevision state
				mockRepo.EXPECT().
					GetLatestRevision(gomock.Any()).
					Return(defaultInitCommit, nil).
					AnyTimes()

				// The controller will call GetChangedFiles to check if governance path is empty
				// Return files to simulate non-empty governance folder
				mockRepo.EXPECT().
					GetChangedFiles(gomock.Any(), "", defaultInitCommit, ".qubmango/.qubmango").
					Return([]governancev1alpha1.FileChange{
						{Kind: "Secret", Name: "existing-file", Path: ".qubmango/.qubmango/existing-file.yaml"},
					}, nil, nil).
					AnyTimes()

				// Create MRT
				mrt := defaultMRT.DeepCopy()
				Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

				// Wait for controller to attempt reconciliation
				time.Sleep(500 * time.Millisecond)

				// VERIFY: Finalizer should NOT be added
				Eventually(func() []string {
					updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
					_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
					return updatedMRT.Finalizers
				}, timeout, interval).Should(BeEmpty(), "Finalizer should not be added when governance path is not empty")

				//ActionState should be stuck in CheckGovernancePathEmpty
				Eventually(func() governancev1alpha1.MRTActionState {
					updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
					_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
					return updatedMRT.Status.ActionState
				}, timeout, interval).Should(Equal(governancev1alpha1.MRTActionStateCheckGovernancePathEmpty))
			})
		})

		Describe("Positive Scenario: Successful initialization", func() {
			It("should complete full initialization flow and add finalizer", func() {
				// SETUP: Create Application
				app := defaultApp.DeepCopy()
				Expect(k8sClient.Create(ctx, app)).Should(Succeed())

				// Mock repository operations for successful initialization
				// Using AnyTimes() because controller runs asynchronously and may retry
				mockRepoManager.EXPECT().
					GetProviderForMRT(gomock.Any(), gomock.Any()).
					Return(mockRepo, nil).
					AnyTimes()

				// State 1: SaveArgoCDTargetRevision - get latest revision
				mockRepo.EXPECT().
					GetLatestRevision(gomock.Any()).
					Return(defaultInitCommit, nil).
					AnyTimes()

				// State 2: CheckGovernancePathEmpty - verify empty governance folder
				mockRepo.EXPECT().
					GetChangedFiles(gomock.Any(), "", defaultInitCommit, ".qubmango/.qubmango").
					Return([]governancev1alpha1.FileChange{}, nil, nil).
					AnyTimes()

				// State 3: CreateDefaultClusterResources - get changed files for MSR/MCA
				mockRepo.EXPECT().
					GetChangedFiles(gomock.Any(), "", defaultInitCommit, "app-manifests").
					Return(defaultRepoChanges, nil, nil).
					AnyTimes()

				// Create MRT with creation commit annotation
				mrt := defaultMRT.DeepCopy()
				mrt.Annotations = map[string]string{
					QubmangoMRTCreationCommitAnnotation: defaultInitCommit,
				}
				Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

				// ACT: Wait for controller to complete initialization (runs in background)
				// We don't need to manually call reconcile since controller is already running

				// VERIFY: Finalizer should be added
				By("Verifying finalizer is added")
				Eventually(func() []string {
					updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
					_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
					return updatedMRT.Finalizers
				}, timeout, interval).Should(ContainElement(GovernanceFinalizer))

				// ActionState should return to Empty after successful initialization
				By("Verifying ActionState returns to Empty")
				Eventually(func() governancev1alpha1.MRTActionState {
					updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
					_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
					return updatedMRT.Status.ActionState
				}, timeout, interval).Should(Equal(governancev1alpha1.MRTActionStateEmpty))

				// Get the final MRT state for subsequent assertions
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				Expect(k8sClient.Get(ctx, mrtKey, updatedMRT)).To(Succeed())
				By("Verifying Application targetRevision is saved")
				Expect(updatedMRT.Status.ApplicationInitTargetRevision).To(Equal("main"))

				// MSR should be created
				By("Verifying MSR is created")
				createdMSR := &governancev1alpha1.ManifestSigningRequest{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{
						Name:      MSRName,
						Namespace: governanceNamespace.Name,
					}, createdMSR)
				}, timeout, interval).Should(Succeed())

				Expect(createdMSR.Spec.Version).To(Equal(0))
				Expect(createdMSR.Spec.CommitSHA).To(Equal(defaultInitCommit))
				Expect(createdMSR.Spec.MRT.Name).To(Equal(MRTName))
				Expect(createdMSR.Spec.Changes).To(HaveLen(2))
				Expect(createdMSR.OwnerReferences).To(HaveLen(1))
				Expect(createdMSR.OwnerReferences[0].Name).To(Equal(MRTName))

				// MCA should be created
				By("Verifying MCA is created")
				createdMCA := &governancev1alpha1.ManifestChangeApproval{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{
						Name:      MCAName,
						Namespace: governanceNamespace.Name,
					}, createdMCA)
				}, timeout, interval).Should(Succeed())

				Expect(createdMCA.Spec.Version).To(Equal(0))
				Expect(createdMCA.Spec.CommitSHA).To(Equal(defaultInitCommit))
				Expect(createdMCA.Spec.MRT.Name).To(Equal(MRTName))
				Expect(createdMCA.Spec.MSR.Name).To(Equal(MSRName))
				Expect(createdMCA.OwnerReferences).To(HaveLen(1))
				Expect(createdMCA.OwnerReferences[0].Name).To(Equal(MRTName))

				// GovernanceQueue should be created
				By("Verifying GovernanceQueue is created")
				createdQueue := &governancev1alpha1.GovernanceQueue{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{
						Name:      QueueName,
						Namespace: governanceNamespace.Name,
					}, createdQueue)
				}, timeout, interval).Should(Succeed())

				Expect(createdQueue.Spec.MRT.Name).To(Equal(MRTName))
				Expect(createdQueue.OwnerReferences).To(HaveLen(1))
				Expect(createdQueue.OwnerReferences[0].Name).To(Equal(MRTName))

				// RevisionQueueRef should be set in MRT status
				By("Verifying RevisionQueueRef is set")
				Expect(updatedMRT.Status.RevisionQueueRef.Name).To(Equal(QueueName))
				Expect(updatedMRT.Status.RevisionQueueRef.Namespace).To(Equal(governanceNamespace.Name))
			})
		})
	})
})
