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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: maybe have TrackedFolder, MsrFolder, SignaturesFolder, ApprovalFolder optional and specify later defaults?
type GitRepository struct {
	// URL to the git repository
	URL string `json:"url,omitempty"`

	// Branch to monitor for infrastructure changes.
	Branch string `json:"branch,omitempty"`

	// Folders RegExp within the repo to monitor for infrastructure changes.
	TrackedFolders []string `json:"trackedFolder,omitempty"`
}


type MSR struct {

	Name string `json:"name,omitempty"`
	
	Namespace string `json:"namespace,omitempty"`

	// Folder within the git repository where MSRs are stored.
	Folder string `json:"folder,omitempty"`

}
type SlackChannel struct {

	// Slack user group ID to notify (e.g., S01234567)
	UserGroupID string `json:"userGroupID,omitempty"`
}

type NotificationChannel struct {
	Slack *SlackChannel `json:"slack,omitempty"`

	// Other notification channels can be added in the future
	// ...

}

type Governor struct {

	// Alias for governor (for easier identification)
	Alias string `json:"alias,omitempty"`

	// PublicKey of the governor used to verify signatures.
	PublicKey string `json:"publicKey,omitempty"`

	// Notification channel to inform the governor about pending approvals.
	NotificationChannel NotificationChannel `json:"notificationChannel"`
}

/*
ApprovalRule defines nested rules for approvals required.
Each rule is a node. Each node can be:
- intermediate node. Contains Require field with child rules:
  - AtLeast: number of child rules that must be satisfied
  - All: if true, all child rules must be satisfied

- leaf node. Doesn't contain Require field, but contains Signer field:
  - Signer: the signer (governor) whose approval is required
*/
type ApprovalRule struct {

	// Specifies the minimum number of child rules that must be satisfied.
	AtLeast 		int `json:"atLeast,omitempty"`

	// If true, all child rules must be satisfied.
	All 			bool `json:"all,omitempty"`

	// List of child rules.
	Require 		[]ApprovalRule `json:"require,omitempty"`

	// Signer can be either PublicKey or Alias of the governor. If alias is used, it should start with `$` to distinguish it from PublicKey.
	Signer 			string `json:"signer,omitempty"`
}

// ManifestRequestTemplateSpec defines the desired state of ManifestRequestTemplate
type ManifestRequestTemplateSpec struct {

	// publicKey is used to sign MCA.
	PublicKey string `json:"publicKey,omitempty"`
	// TODO: make PublicKeyRef in future to reference to a secret

	// gitRepository specifies the git repository configuration for this ManifestRequestTemplate
	GitRepository GitRepository `json:"gitRepository"`

	MSR MSR `json:"msr,omitempty"`

	Governors []Governor `json:"governors,omitempty"`
	//TODO: make GovernorsRef in future to reference to a governor manifest

	// The policy rules for approvals.
	Require ApprovalRule `json:"require"`

}

// ManifestRequestTemplateStatus defines the observed state of ManifestRequestTemplate.
type ManifestRequestTemplateStatus struct {

	// conditions represent the current state of the ManifestRequestTemplate resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	LastObservedCommitHash string `json:"lastObservedCommitHash,omitempty"`

	LastManifestSigningRequest string `json:"lastManifestSigningRequest,omitempty"`

	LastAcceptedManifestSigningRequest string `json:"lastAcceptedManifestSigningRequest,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManifestRequestTemplate is the Schema for the manifestrequesttemplates API
type ManifestRequestTemplate struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ManifestRequestTemplate
	// +required
	Spec ManifestRequestTemplateSpec `json:"spec"`

	// status defines the observed state of ManifestRequestTemplate
	// +optional
	Status ManifestRequestTemplateStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ManifestRequestTemplateList contains a list of ManifestRequestTemplate
type ManifestRequestTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManifestRequestTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ManifestRequestTemplate{}, &ManifestRequestTemplateList{})
}
