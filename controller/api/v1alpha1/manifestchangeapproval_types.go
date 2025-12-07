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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ManifestChangeApprovalSpec defines the desired state of ManifestChangeApproval
type ManifestChangeApprovalSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// foo is an example field of ManifestChangeApproval. Edit manifestchangeapproval_types.go to remove/update
	// +optional
	Foo *string `json:"foo,omitempty"`
}

type ManifestChangeApprovalHistoryRecord struct {
	// CommitSHA is the SHA of the approved commit
	CommitSHA string `json:"commitSHA"`

	// Time is the time when the approval was created
	Time metav1.Time `json:"time"`
}

// ManifestChangeApprovalStatus defines the observed state of ManifestChangeApproval.
type ManifestChangeApprovalStatus struct {

	// Last approved commit SHA
	// +optional
	LastApprovedCommitSHA string `json:"lastApprovedCommitSHA,omitempty"`

	// History of approvals
	// +required
	ApprovalHistory []ManifestChangeApprovalHistoryRecord `json:"approvalHistory,omitempty"`

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
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManifestChangeApproval is the Schema for the manifestchangeapprovals API
type ManifestChangeApproval struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ManifestChangeApproval
	// +required
	Spec ManifestChangeApprovalSpec `json:"spec"`

	// status defines the observed state of ManifestChangeApproval
	// +optional
	Status ManifestChangeApprovalStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ManifestChangeApprovalList contains a list of ManifestChangeApproval
type ManifestChangeApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ManifestChangeApproval `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ManifestChangeApproval{}, &ManifestChangeApprovalList{})
}
