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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var manifestchangeapprovallog = logf.Log.WithName("manifestchangeapproval-resource")

// SetupManifestChangeApprovalWebhookWithManager registers the webhook for ManifestChangeApproval in the manager.
func SetupManifestChangeApprovalWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&governancev1alpha1.ManifestChangeApproval{}).
		WithValidator(&ManifestChangeApprovalCustomValidator{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-governance-nazar-grynko-com-v1alpha1-manifestchangeapproval,mutating=false,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=create;update,versions=v1alpha1,name=vmanifestchangeapproval-v1alpha1.kb.io,admissionReviewVersions=v1

// ManifestChangeApprovalCustomValidator struct is responsible for validating the ManifestChangeApproval resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ManifestChangeApprovalCustomValidator struct {
	// TODO(user): Add more fields as needed for validation
}

var _ webhook.CustomValidator = &ManifestChangeApprovalCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type ManifestChangeApproval.
func (v *ManifestChangeApprovalCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	manifestchangeapproval, ok := obj.(*governancev1alpha1.ManifestChangeApproval)
	if !ok {
		return nil, fmt.Errorf("expected a ManifestChangeApproval object but got %T", obj)
	}
	manifestchangeapprovallog.Info("Validation for ManifestChangeApproval upon creation", "name", manifestchangeapproval.GetName())

	// TODO(user): fill in your validation logic upon object creation.

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type ManifestChangeApproval.
func (v *ManifestChangeApprovalCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	manifestchangeapproval, ok := newObj.(*governancev1alpha1.ManifestChangeApproval)
	if !ok {
		return nil, fmt.Errorf("expected a ManifestChangeApproval object for the newObj but got %T", newObj)
	}
	manifestchangeapprovallog.Info("Validation for ManifestChangeApproval upon update", "name", manifestchangeapproval.GetName())

	// TODO(user): fill in your validation logic upon object update.

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type ManifestChangeApproval.
func (v *ManifestChangeApprovalCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	manifestchangeapproval, ok := obj.(*governancev1alpha1.ManifestChangeApproval)
	if !ok {
		return nil, fmt.Errorf("expected a ManifestChangeApproval object but got %T", obj)
	}
	manifestchangeapprovallog.Info("Validation for ManifestChangeApproval upon deletion", "name", manifestchangeapproval.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
