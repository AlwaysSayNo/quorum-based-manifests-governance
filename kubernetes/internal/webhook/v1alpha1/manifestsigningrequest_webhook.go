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
	"reflect"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

// +kubebuilder:webhook:path=/msr/validate,mutating=false,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestsigningrequests,verbs=create;update,versions=v1alpha1,name=msr-validating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1

type ManifestSigningRequestCustomValidator struct {
	Client client.Client
	logger logr.Logger
}

func (v *ManifestSigningRequestCustomValidator) ValidateCreate(
	ctx context.Context,
	obj runtime.Object,
) (admission.Warnings, error) {
	return nil, nil
}

func (v *ManifestSigningRequestCustomValidator) ValidateUpdate(
	ctx context.Context,
	oldObj, newObj runtime.Object,
) (admission.Warnings, error) {
	newMSR := newObj.(*governancev1alpha1.ManifestSigningRequest)
	oldMSR := oldObj.(*governancev1alpha1.ManifestSigningRequest)

	var allErrs field.ErrorList

	specEqual := reflect.DeepEqual(oldMSR.Spec, newMSR.Spec)

	// Validate, that on MSR spec change, version incremented as well
	if !specEqual && newMSR.Spec.Version <= oldMSR.Spec.Version {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("version"), newMSR.Spec.Version, "version must be incremented on change"))
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(newMSR.GroupVersionKind().GroupKind(), newMSR.Name, allErrs)
}

func (v *ManifestSigningRequestCustomValidator) ValidateDelete(
	ctx context.Context,
	obj runtime.Object,
) (admission.Warnings, error) {
	return nil, nil
}
