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
	"strings"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// nolint:unused
// log is for logging in this package.
var manifestchangeapprovallog = logf.Log.WithName("manifestchangeapproval-resource")

// +kubebuilder:webhook:path=/validate-all,mutating=false,failurePolicy=fail,sideEffects=None,groups=*,resources=*,verbs=create;update;delete,versions=*,name=mca-governance.kb.io,admissionReviewVersions=v1

// ManifestChangeApprovalCustomValidator struct is responsible for validating the ManifestChangeApproval resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ManifestChangeApprovalCustomValidator struct {
	Client  client.Client
	Decoder *admission.Decoder
}

// Constants for policy
const argoCDServiceAccountName = "argocd-application-controller"
const argoCDNamespace = "argocd"
const argoCDUsername = "system:serviceaccount:" + argoCDNamespace + ":" + argoCDServiceAccountName

// Handle is the core function that gets called for every admission request.
func (v *ManifestChangeApprovalCustomValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := logf.FromContext(ctx).WithValues("user", req.UserInfo.Username)
	logger.Info("Handling request in generic webhook")

	if strings.HasPrefix(req.UserInfo.Username, "system:serviceaccount:") {
		logger.Info("Request from a ServiceAccount", "username", req.UserInfo.Username)
		return admission.Allowed("Allowed SA for now")
	}

	logger.Info("Request form a non-ServiceAccount user", "username", req.UserInfo.Username)
	return admission.Allowed("Allowed non-SA for now")
}

// InjectDecoder injects the decoder.
func (v *ManifestChangeApprovalCustomValidator) InjectDecoder(d *admission.Decoder) error {
	v.Decoder = d
	return nil
}
