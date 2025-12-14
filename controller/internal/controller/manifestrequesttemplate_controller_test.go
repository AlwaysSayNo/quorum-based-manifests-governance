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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	. "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/internal/controller"
	controllermocks "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/internal/controller/mocks"
	managermocks "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/internal/repository/mocks"
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
		governanceNamespace *corev1.Namespace
		argoCDNamespace     *corev1.Namespace
		defaultMSR          governancev1alpha1.ManifestRequestTemplate
		defaultApp          argocdv1alpha1.Application
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockRepoManager = controllermocks.NewMockRepositoryManager(mockCtrl)
		mockRepo = managermocks.NewMockGitRepository(mockCtrl)
		mrtReconciler.RepoManager = mockRepoManager

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

		// Create default bare minimum MSR
		defaultMSR = governancev1alpha1.ManifestRequestTemplate{
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

		// Create dependent Application
		defaultApp = argocdv1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: argoCDNamespace.Name},
			Spec: argocdv1alpha1.ApplicationSpec{
				Source: &argocdv1alpha1.ApplicationSource{RepoURL: TestRepoURL, Path: "app-manifests"},
			},
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Context("Reconciliation Lifecycle", func() {
		It("should remove the finalizer when an MRT is deleted", func() {
			// SETUP
			ctx := context.Background()
			mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}

			// Mock manager and repository calls
			mockLatestRevision := "abc123def456"
			mockChangedFiles := []governancev1alpha1.FileChange{
				{Kind: "Deployment", Status: governancev1alpha1.New, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"},
				{Kind: "ManifestRequestTemplate", Status: governancev1alpha1.New, Name: "test-mrt", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/mrt.yaml"},
			}

			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()

			mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(mockLatestRevision, nil).AnyTimes()
			mockRepo.EXPECT().GetChangedFiles(gomock.Any(), "", mockLatestRevision, gomock.Any()).Return(mockChangedFiles, nil).AnyTimes()

			// Create Application before MRT
			app := &defaultApp
			Expect(k8sClient.Create(ctx, app)).Should(Succeed())

			// Create MRT
			mrt := &defaultMSR
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
			ctx := context.Background()

			// Don't setup the Application

			mrt := &defaultMSR
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

		It("should initialize an MRT by adding a finalizer and creating default MSR and MCA", func() {
			// SETUP
			ctx := context.Background()
			mrtKey := types.NamespacedName{Name: MRTName, Namespace: governanceNamespace.Name}

			// Mock manager and repository calls
			mockLatestRevision := "abc123def456"
			mockChangedFiles := []governancev1alpha1.FileChange{
				{Kind: "Deployment", Status: governancev1alpha1.New, Name: "my-app", Namespace: "my-ns", SHA256: "some", Path: "app-manifests/deployment.yaml"},
			}

			// Without AnyTimes
			mockRepoManager.EXPECT().GetProviderForMRT(gomock.Any(), gomock.Any()).Return(mockRepo, nil).AnyTimes()
			mockRepo.EXPECT().GetLatestRevision(gomock.Any()).Return(mockLatestRevision, nil).AnyTimes()
			mockRepo.EXPECT().GetChangedFiles(gomock.Any(), "", mockLatestRevision, "app-manifests").Return(mockChangedFiles, nil).AnyTimes()

			// Create Application before MRT
			app := &defaultApp
			Expect(k8sClient.Create(ctx, app)).Should(Succeed())

			// Create MRT
			mrt := &defaultMSR
			Expect(k8sClient.Create(ctx, mrt)).Should(Succeed())

			// ACT + ASSERT
			// Check MRT exists
			By("ensuring the finalizer is added")
			Eventually(func() []string {
				updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
				_ = k8sClient.Get(ctx, mrtKey, updatedMRT)
				return updatedMRT.Finalizers
			}, timeout, interval).Should(ContainElement(MRTFinalizer))

			// Check MSR exists
			By("ensuring the default MSR is created")
			createdMSR := &governancev1alpha1.ManifestSigningRequest{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: MSRName, Namespace: governanceNamespace.Name}, createdMSR)
			}, timeout, interval).Should(Succeed())
			Expect(createdMSR.Spec.Changes).To(HaveLen(1))
			Expect(createdMSR.Spec.Changes[0].Name).To(Equal("my-app"))
			Expect(createdMSR.OwnerReferences).To(HaveLen(1), "MSR should be owned by the MRT")
			Expect(createdMSR.OwnerReferences[0].Name).To(Equal(MRTName))
			Expect(createdMSR.Spec.Version).To(Equal(0)) // expect version 0
			Expect(createdMSR.Spec.MRT.Name).To(Equal(MRTName))
			Expect(createdMSR.Spec.MRT.Namespace).To(Equal(governanceNamespace.Name))
			Expect(createdMSR.Spec.MRT.Version).To(Equal(defaultMSR.Spec.Version))
			Expect(createdMSR.Spec.PublicKey).To(Equal(defaultMSR.Spec.PGP.PublicKey))
			Expect(createdMSR.Spec.GitRepository.URL).To(Equal(TestRepoURL))
			Expect(createdMSR.Spec.Status).To(Equal(governancev1alpha1.Approved))
			Expect(createdMSR.Spec.Governors).To(Equal(defaultMSR.Spec.Governors))
			Expect(createdMSR.Spec.Require).To(Equal(defaultMSR.Spec.Require))
			Expect(createdMSR.Status.RequestHistory).To(HaveLen(1))

			// Check MCA exists
			By("ensuring the default MCA is created")
			createdMCA := &governancev1alpha1.ManifestChangeApproval{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: MCAName, Namespace: governanceNamespace.Name}, createdMCA)
			}, timeout, interval).Should(Succeed())
			By(fmt.Sprintf("%#v\n", createdMCA.Spec))
			By(fmt.Sprintf("%#v\n", createdMCA.Status))
			Expect(createdMCA.Status.LastApprovedCommitSHA).To(Equal(mockLatestRevision))
			Expect(createdMCA.OwnerReferences).To(HaveLen(1), "MCA should be owned by the MRT")
			Expect(createdMCA.Spec.MRT).To(Equal(createdMSR.Spec.MRT))
			Expect(createdMCA.Spec.MSR.Name).To(Equal(MSRName))
			Expect(createdMCA.Spec.MSR.Namespace).To(Equal(governanceNamespace.Name))
			Expect(createdMCA.Spec.MSR.Version).To(Equal(0))
			Expect(createdMCA.Spec.PublicKey).To(Equal(defaultMSR.Spec.PGP.PublicKey))
			Expect(createdMCA.Spec.GitRepository.URL).To(Equal(TestRepoURL))
			Expect(createdMCA.Spec.Governors).To(Equal(defaultMSR.Spec.Governors))
			Expect(createdMCA.Spec.Require).To(Equal(defaultMSR.Spec.Require))
			Expect(createdMCA.Spec.Changes).To(Equal(createdMSR.Spec.Changes))
			Expect(createdMCA.Status.ApprovalHistory).To(HaveLen(1))

			// Check MRT is updated
			By("ensuring the MRT status is updated")
			updatedMRT := &governancev1alpha1.ManifestRequestTemplate{}
			Expect(k8sClient.Get(ctx, mrtKey, updatedMRT)).To(Succeed())
			Expect(updatedMRT.Status.LastObservedCommitHash).To(Equal(mockLatestRevision))
			Expect(updatedMRT.Status.LastMSRVersion).To(Equal(0)) // expect version 0
		})
	})
})
