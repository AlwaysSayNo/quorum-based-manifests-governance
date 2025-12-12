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

// ManifestChangeApprovalSpec defines the desired state of ManifestChangeApproval
type ManifestChangeApprovalSpec struct {
	// 0 is value of the default ManifestSigningRequest, created by qubmango
	// +kubebuilder:validation:Minimum=0
	// +required
	Version int `json:"version,omitempty"`

	// +required
	MRT VersionedManifestRef `json:"mrt,omitempty"`

	// +required
	MSR VersionedManifestRef `json:"msr,omitempty"`

	// publicKey is used to sign MCA.
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty"`

	// +required
	GitRepository GitRepository `json:"gitRepository"`

	// Location contains information about where to store MSR, MCA and signatures.
	// +required
	Location Location `json:"location,omitempty"`

	// +required
	Changes []FileChange `json:"changes"`

	// Required until GovernorsRef is implemented.
	// +required
	Governors GovernorList `json:"governors,omitempty"`

	// The policy rules for approvals.
	// Required until ApprovalRuleRef is implemented.
	// +required
	Require ApprovalRule `json:"require"`

	// Signers contains all governors, who signed ManifestSigningRequest, related to this approval
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
