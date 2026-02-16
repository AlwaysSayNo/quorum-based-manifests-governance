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

package v1alpha1

import (
	"context"

	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

var (
	testScheme = func() *runtime.Scheme {
		s := runtime.NewScheme()
		_ = governancev1alpha1.AddToScheme(s)
		_ = argoappv1.AddToScheme(s)
		return s
	}()
)

var _ = Describe("ManifestRequestTemplate Webhook", func() {
	var (
		ctx       context.Context
		validator ManifestRequestTemplateWebhook
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("ValidateCreate", func() {
		It("should fail when ArgoCD Application namespace is not default", func() {
			// SETUP
			app := newApplication("my-app", "custom", "git@github.com:acme/repo.git")
			client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
			validator = ManifestRequestTemplateWebhook{Client: client}
			mrt := newMRT("mrt-1", "governance", "my-app", "custom", "git@github.com:acme/repo.git", []string{"$owner"},
				governancev1alpha1.ApprovalRule{Signer: "$owner"})

			// ACT
			warnings, err := validator.ValidateCreate(ctx, mrt)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("dynamic namespaces are not supported"))
		})

		It("should fail when nested approval rules are invalid", func() {
			// SETUP
			app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
			client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
			validator = ManifestRequestTemplateWebhook{Client: client}
			mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git", []string{"$bob"},
				governancev1alpha1.ApprovalRule{Signer: "$alice"})

			// ACT
			warnings, err := validator.ValidateCreate(ctx, mrt)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("signer doesn't exist in governors list"))
		})

		It("should fail when MRT and Application repo URLs do not match", func() {
			// SETUP
			app := newApplication("my-app", "argocd", "git@github.com:acme/other.git")
			client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
			validator = ManifestRequestTemplateWebhook{Client: client}
			mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git", []string{"$owner"},
				governancev1alpha1.ApprovalRule{Signer: "$owner"})

			// ACT
			warnings, err := validator.ValidateCreate(ctx, mrt)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("SSH repository URL must have the same host, organization and name"))
		})

		It("should allow creation when inputs are valid", func() {
			// SETUP
			app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
			client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
			validator = ManifestRequestTemplateWebhook{Client: client}
			mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git", []string{"$owner"},
				governancev1alpha1.ApprovalRule{Signer: "$owner"})

			// ACT
			warnings, err := validator.ValidateCreate(ctx, mrt)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).NotTo(HaveOccurred())
		})

		Context("Approval Rule Validation", func() {
			It("should accept valid nested rules with atLeast", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast := 2
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob", "$charlie"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast,
						Require: []governancev1alpha1.ApprovalRule{
							{Signer: "$alice"},
							{Signer: "$bob"},
							{Signer: "$charlie"},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept valid nested rules with all=true", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				all := true
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob"},
					governancev1alpha1.ApprovalRule{
						All: &all,
						Require: []governancev1alpha1.ApprovalRule{
							{Signer: "$alice"},
							{Signer: "$bob"},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when both atLeast and all are set", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast := 1
				all := true
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast,
						All:     &all,
						Require: []governancev1alpha1.ApprovalRule{
							{Signer: "$alice"},
							{Signer: "$bob"},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("atLeast and all cannot be set in the same time"))
			})

			It("should reject when both atLeast and all are null with non-empty require", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice"},
					governancev1alpha1.ApprovalRule{
						Require: []governancev1alpha1.ApprovalRule{
							{Signer: "$alice"},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("atLeast and all cannot be null in the same time"))
			})

			It("should reject signer on non-leaf node with require", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast := 1
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast,
						Signer:  "$alice", // Invalid: signer on non-leaf
						Require: []governancev1alpha1.ApprovalRule{
							{Signer: "$bob"},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("signer should be placed only on a leaf node"))
			})

			It("should reject signer on non-leaf node with atLeast", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast := 1
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast,
						Signer:  "$alice", // Invalid: signer with atLeast
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("signer should be placed only on a leaf node"))
			})

			It("should reject signer on non-leaf node with all", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				all := true
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice"},
					governancev1alpha1.ApprovalRule{
						All:    &all,
						Signer: "$alice", // Invalid: signer with all
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("signer should be placed only on a leaf node"))
			})

			It("should reject inner node with empty require", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast := 1
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast,
						Require: []governancev1alpha1.ApprovalRule{}, // Empty
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("no rule or signer is set"))
			})

			It("should reject when atLeast is not reachable", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast := 5 // Requires 5 but only 2 children
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast,
						Require: []governancev1alpha1.ApprovalRule{
							{Signer: "$alice"},
							{Signer: "$bob"},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("atLeast is not reachable"))
			})

			It("should accept complex multi-level nested rules", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast2 := 2
				atLeast1 := 1
				allTrue := true
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob", "$charlie", "$david"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast1,
						Require: []governancev1alpha1.ApprovalRule{
							{
								AtLeast: &atLeast2,
								Require: []governancev1alpha1.ApprovalRule{
									{Signer: "$alice"},
									{Signer: "$bob"},
									{Signer: "$charlie"},
								},
							},
							{
								All: &allTrue,
								Require: []governancev1alpha1.ApprovalRule{
									{Signer: "$david"},
									{Signer: "$alice"},
								},
							},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject nested rule with invalid signer at deeper level", func() {
				// SETUP
				app := newApplication("my-app", "argocd", "git@github.com:acme/repo.git")
				client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(app).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				atLeast1 := 1
				atLeast2 := 2
				mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
					[]string{"$alice", "$bob"},
					governancev1alpha1.ApprovalRule{
						AtLeast: &atLeast1,
						Require: []governancev1alpha1.ApprovalRule{
							{
								AtLeast: &atLeast2,
								Require: []governancev1alpha1.ApprovalRule{
									{Signer: "$alice"},
									{Signer: "$bob"},
									{Signer: "$invalidUser"}, // Invalid signer at deeper level
								},
							},
						},
					})

				// ACT
				warnings, err := validator.ValidateCreate(ctx, mrt)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("signer doesn't exist in governors list"))
			})
		})
	})

	Describe("ValidateUpdate", func() {
		var oldMRT, updatedMRT *governancev1alpha1.ManifestRequestTemplate

		BeforeEach(func() {
			oldMRT = newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git",
				[]string{"$alice", "$bob"},
				governancev1alpha1.ApprovalRule{Signer: "$alice"})
			oldMRT.Spec.Version = 1

			updatedMRT = oldMRT.DeepCopy()
		})

		Context("Version Validation", func() {
			It("should accept update when spec unchanged and version unchanged", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept update when spec changed and version incremented", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.Governors.Members = append(updatedMRT.Spec.Governors.Members,
					governancev1alpha1.Governor{Alias: "$charlie", PublicKey: "PUB3"})

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject update when spec changed but version not incremented", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Governors.Members = append(updatedMRT.Spec.Governors.Members,
					governancev1alpha1.Governor{Alias: "$charlie", PublicKey: "PUB3"})

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("version must be incremented on change"))
			})

			It("should reject update when spec changed but version decremented", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				oldMRT.Spec.Version = 5
				updatedMRT.Spec.Version = 3
				updatedMRT.Spec.Governors.Members = append(updatedMRT.Spec.Governors.Members,
					governancev1alpha1.Governor{Alias: "$charlie", PublicKey: "PUB3"})

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("version must be incremented on change"))
			})
		})

		Context("Immutable Fields Validation", func() {
			It("should reject when gitRepository.ssh is changed", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.GitRepository.SSH.URL = "git@github.com:other/repo.git"

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("ssh is immutable"))
			})

			It("should reject when pgp is changed", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.PGP.PublicKey = "NEW_PGP_KEY"

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("pgp is immutable"))
			})

			It("should reject when argoCD application is changed", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.ArgoCD.Application.Name = "different-app"

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("argoCD is immutable"))
			})

			It("should reject when MSR reference is changed", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.MSR.Name = "different-msr"

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("MSR reference is immutable"))
			})

			It("should reject when MCA reference is changed", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.MCA.Name = "different-mca"

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("MCA reference is immutable"))
			})

			It("should reject when slack notification is changed", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				oldMRT.Spec.Notifications = &governancev1alpha1.NotificationConfig{
					Slack: &governancev1alpha1.SlackSecret{
						SecretsRef: governancev1alpha1.ManifestRef{Name: "slack-secret", Namespace: "governance"},
					},
				}
				updatedMRT = oldMRT.DeepCopy()
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.Notifications.Slack.SecretsRef.Name = "different-slack-secret"

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("slack is immutable"))
			})

			It("should accept when argoCD namespace is argocd", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				updatedMRT.Spec.Governors.Members = append(updatedMRT.Spec.Governors.Members,
					governancev1alpha1.Governor{Alias: "$charlie", PublicKey: "PUB3"})

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when argoCD namespace is not argocd", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				oldMRT.Spec.ArgoCD.Application.Namespace = "custom"
				updatedMRT = oldMRT.DeepCopy()
				updatedMRT.Spec.Version = 2

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("dynamic namespaces are not supported"))
			})
		})

		Context("Approval Rule Validation", func() {
			It("should validate approval rules on update", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}
				updatedMRT.Spec.Version = 2
				// Add invalid signer that doesn't exist in governors
				updatedMRT.Spec.Require = governancev1alpha1.ApprovalRule{Signer: "$nonexistent"}

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("signer doesn't exist in governors list"))
			})
		})

		Context("Multiple Validation Errors", func() {
			It("should report multiple errors at once", func() {
				// SETUP
				client := fake.NewClientBuilder().WithScheme(testScheme).Build()
				validator = ManifestRequestTemplateWebhook{Client: client}

				// Change spec without incrementing version
				updatedMRT.Spec.GitRepository.SSH.URL = "git@github.com:other/repo.git"
				updatedMRT.Spec.PGP.PublicKey = "NEW_PGP"
				updatedMRT.Spec.Governors.Members = append(updatedMRT.Spec.Governors.Members,
					governancev1alpha1.Governor{Alias: "$charlie", PublicKey: "PUB3"})

				// ACT
				warnings, err := validator.ValidateUpdate(ctx, oldMRT, updatedMRT)

				// VERIFY
				Expect(warnings).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				// Should contain multiple errors
				Expect(err.Error()).To(ContainSubstring("version must be incremented"))
				Expect(err.Error()).To(ContainSubstring("ssh is immutable"))
				Expect(err.Error()).To(ContainSubstring("pgp is immutable"))
			})
		})
	})

	Describe("SameGitRepository", func() {
		Context("SSH to SSH comparisons", func() {
			It("should match identical SSH URLs", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "git@github.com:acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match SSH URLs with and without .git suffix", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "git@github.com:acme/repo")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match SSH URLs with different case", func() {
				same, err := SameGitRepository("git@github.com:Acme/Repo.git", "git@github.com:acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should not match different repositories", func() {
				same, err := SameGitRepository("git@github.com:acme/repo1.git", "git@github.com:acme/repo2.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})

			It("should not match different organizations", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "git@github.com:other/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})

			It("should not match different hosts", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "git@gitlab.com:acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})

			It("should reject invalid SSH URL format", func() {
				_, err := SameGitRepository("git@github.com-invalid", "git@github.com:acme/repo.git")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid SSH URL"))
			})
		})

		Context("SSH to HTTPS comparisons", func() {
			It("should match SSH and HTTPS URLs for same repo", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "https://github.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match SSH and HTTPS URLs without .git suffix", func() {
				same, err := SameGitRepository("git@github.com:acme/repo", "https://github.com/acme/repo")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match with mixed .git suffix presence", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "https://github.com/acme/repo")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match with case insensitivity", func() {
				same, err := SameGitRepository("git@GitHub.com:ACME/Repo.git", "https://github.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should not match different repos", func() {
				same, err := SameGitRepository("git@github.com:acme/repo1.git", "https://github.com/acme/repo2.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})
		})

		Context("HTTPS to HTTPS comparisons", func() {
			It("should match identical HTTPS URLs", func() {
				same, err := SameGitRepository("https://github.com/acme/repo.git", "https://github.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match HTTPS URLs with and without .git suffix", func() {
				same, err := SameGitRepository("https://github.com/acme/repo.git", "https://github.com/acme/repo")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match HTTPS URLs with different case", func() {
				same, err := SameGitRepository("https://GitHub.com/Acme/Repo.git", "https://github.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should not match different hosts", func() {
				same, err := SameGitRepository("https://github.com/acme/repo.git", "https://gitlab.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})

			It("should not match different organizations", func() {
				same, err := SameGitRepository("https://github.com/acme/repo.git", "https://github.com/other/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})
		})

		Context("ssh:// protocol format", func() {
			It("should match ssh:// with git@ format", func() {
				same, err := SameGitRepository("ssh://git@github.com/acme/repo.git", "git@github.com:acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match ssh:// with HTTPS format", func() {
				same, err := SameGitRepository("ssh://git@github.com/acme/repo.git", "https://github.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should match identical ssh:// URLs", func() {
				same, err := SameGitRepository("ssh://git@github.com/acme/repo.git", "ssh://git@github.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should handle ssh:// without user", func() {
				same, err := SameGitRepository("ssh://github.com/acme/repo.git", "git@github.com:acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})
		})

		Context("Edge cases", func() {
			It("should handle URLs with trailing/leading spaces", func() {
				same, err := SameGitRepository("  git@github.com:acme/repo.git  ", "git@github.com:acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should handle nested repository paths", func() {
				same, err := SameGitRepository("git@github.com:acme/team/repo.git", "https://github.com/acme/team/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should handle GitLab-style SSH URLs", func() {
				same, err := SameGitRepository("git@gitlab.com:acme/repo.git", "https://gitlab.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should handle Bitbucket-style URLs", func() {
				same, err := SameGitRepository("git@bitbucket.org:acme/repo.git", "https://bitbucket.org/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should handle custom Git server URLs", func() {
				same, err := SameGitRepository("git@git.company.com:acme/repo.git", "https://git.company.com/acme/repo.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})

			It("should not match completely different format URLs", func() {
				same, err := SameGitRepository("git@github.com:acme/repo.git", "https://gitlab.com/other/different.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeFalse())
			})

			It("should handle repository names with dashes and underscores", func() {
				same, err := SameGitRepository("git@github.com:acme/my-repo_name.git", "https://github.com/acme/my-repo_name.git")
				Expect(err).NotTo(HaveOccurred())
				Expect(same).To(BeTrue())
			})
		})
	})
})

func newApplication(name, namespace, repoURL string) *argoappv1.Application {
	return &argoappv1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: argoappv1.ApplicationSpec{
			Source: &argoappv1.ApplicationSource{
				RepoURL: repoURL,
			},
		},
	}
}

func newMRT(
	name string,
	namespace string,
	appName string,
	appNamespace string,
	repoURL string,
	governorAliases []string,
	rule governancev1alpha1.ApprovalRule,
) *governancev1alpha1.ManifestRequestTemplate {
	governors := make([]governancev1alpha1.Governor, 0, len(governorAliases))
	for _, alias := range governorAliases {
		governors = append(governors, governancev1alpha1.Governor{Alias: alias, PublicKey: "PUB"})
	}

	return &governancev1alpha1.ManifestRequestTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: governancev1alpha1.ManifestRequestTemplateSpec{
			Version: 1,
			GitRepository: governancev1alpha1.GitRepository{
				SSH: governancev1alpha1.GitSSH{
					URL: repoURL,
					SecretsRef: &governancev1alpha1.ManifestRef{
						Name:      "ssh-secret",
						Namespace: namespace,
					},
				},
			},
			PGP: &governancev1alpha1.PGPPrivateKeySecret{
				PublicKey: "PGP",
				SecretsRef: governancev1alpha1.ManifestRef{
					Name:      "pgp-secret",
					Namespace: namespace,
				},
			},
			ArgoCD: governancev1alpha1.ArgoCD{
				Application: governancev1alpha1.ManifestRef{
					Name:      appName,
					Namespace: appNamespace,
				},
			},
			MSR: governancev1alpha1.ManifestRef{Name: MSRDefaultName, Namespace: namespace},
			MCA: governancev1alpha1.ManifestRef{Name: MCADefaultName, Namespace: namespace},
			Governors: governancev1alpha1.GovernorList{
				Members: governors,
			},
			Require: rule,
		},
	}
}
