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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

var _ = Describe("ManifestSigningRequest Webhook", func() {
	var (
		validator ManifestSigningRequestCustomValidator
		ctx       context.Context
	)

	BeforeEach(func() {
		// SETUP
		validator = ManifestSigningRequestCustomValidator{}
		ctx = context.Background()
	})

	Describe("ValidateUpdate", func() {
		It("should fail when spec changes without version increment", func() {
			// SETUP
			oldObj := newMSR("msr-1", 2, "commit-a")
			newObj := newMSR("msr-1", 2, "commit-b")

			// ACT
			warnings, err := validator.ValidateUpdate(ctx, oldObj, newObj)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("version must be incremented on change"))
		})

		It("should fail when spec changes and version is decremented", func() {
			// SETUP
			oldObj := newMSR("msr-1", 4, "commit-a")
			newObj := newMSR("msr-1", 3, "commit-b")

			// ACT
			warnings, err := validator.ValidateUpdate(ctx, oldObj, newObj)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("version must be incremented on change"))
		})

		It("should allow updates when spec changes and version is incremented", func() {
			// SETUP
			oldObj := newMSR("msr-1", 1, "commit-a")
			newObj := newMSR("msr-1", 2, "commit-b")

			// ACT
			warnings, err := validator.ValidateUpdate(ctx, oldObj, newObj)

			// VERIFY
			Expect(warnings).To(BeNil())
			Expect(err).NotTo(HaveOccurred())
		})
	})

})

func newMSR(name string, version int, commitSHA string) *governancev1alpha1.ManifestSigningRequest {
	return &governancev1alpha1.ManifestSigningRequest{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ManifestSigningRequest",
			APIVersion: fmt.Sprintf("%s/%s", governancev1alpha1.GroupVersion.Group, governancev1alpha1.GroupVersion.Version),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: governancev1alpha1.ManifestSigningRequestSpec{
			Version:   version,
			CommitSHA: commitSHA,
		},
	}
}
