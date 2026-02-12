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

var _ = Describe("ManifestRequestTemplate Webhook", func() {
	var (
		ctx       context.Context
		validator ManifestRequestTemplateWebhook
		scheme    *runtime.Scheme
	)

	BeforeEach(func() {
		// SETUP
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(governancev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(argoappv1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("ValidateCreate", func() {
		It("should fail when ArgoCD Application namespace is not default", func() {
			// SETUP
			app := newApplication("my-app", "custom", "git@github.com:acme/repo.git")
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
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
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
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
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
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
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
			validator = ManifestRequestTemplateWebhook{Client: client}
			mrt := newMRT("mrt-1", "governance", "my-app", "argocd", "git@github.com:acme/repo.git", []string{"$owner"},
				governancev1alpha1.ApprovalRule{Signer: "$owner"})

			// ACT
			warnings, err := validator.ValidateCreate(ctx, mrt)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).NotTo(HaveOccurred())
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
