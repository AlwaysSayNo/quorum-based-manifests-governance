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

type FileChangeStatus string

const (
	New     FileChangeStatus = "New"
	Updated FileChangeStatus = "Updated"
	Deleted FileChangeStatus = "Deleted"
)

type SigningRequestStatus string

const (
	InProgress  SigningRequestStatus = "In Progress"
	NotApproved SigningRequestStatus = "Not Approved"
	Approved    SigningRequestStatus = "Approved"
)

type GitRepository struct {
	// +required
	URL string `json:"url,omitempty"`
}

type VersionedManifestRef struct {
	// +required
	Name string `json:"name"`
	// +required
	Namespace string `json:"namespace"`

	// +required
	Version int `json:"version"`
}

type FileChange struct {
	// +required
	Kind string `json:"kind,omitempty"`
	// +required
	Status FileChangeStatus `json:"status,omitempty"`
	// +required
	Name string `json:"name,omitempty"`
	// +required
	Namespace string `json:"namespace"`
	// +required
	SHA256 string `json:"sha256,omitempty"`
	// +required
	Path string `json:"path"`
}

type ApproverList struct {
	// List of governors.
	// +optional
	Members []Governor `json:"members"`
}

type ManifestSigningRequestHistoryRecord struct {

	// +required
	Version int `json:"version"`

	// +required
	Changes []FileChange `json:"changes"`

	// +required
	Governors GovernorList `json:"governors,omitempty"`

	// +required
	Require ApprovalRule `json:"require"`

	// +optional
	Approves ApproverList `json:"approves"`

	// +required
	Status SigningRequestStatus `json:"status,omitempty"`
}

// ManifestSigningRequestSpec defines the desired state of ManifestSigningRequest
type ManifestSigningRequestSpec struct {
	// 0 is value of the default ManifestSigningRequest, created by qubmango
	// +kubebuilder:validation:Minimum=0
	// +required
	Version int `json:"version"`

	// +optional
	MRT VersionedManifestRef `json:"mrt"`

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

	// +required
	Status SigningRequestStatus `json:"status,omitempty"`
}

// ManifestSigningRequestStatus defines the observed state of ManifestSigningRequest.
type ManifestSigningRequestStatus struct {

	// +required
	RequestHistory []ManifestSigningRequestHistoryRecord `json:"requestHistory,omitempty"`

	// +optional
	Approves ApproverList `json:"approves"`

	// conditions represent the current state of the ManifestSigningRequest resource.
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
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManifestSigningRequest is the Schema for the manifestsigningrequests API
type ManifestSigningRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ManifestSigningRequest
	// +required
	Spec ManifestSigningRequestSpec `json:"spec"`

	// status defines the observed state of ManifestSigningRequest
	// +optional
	Status ManifestSigningRequestStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ManifestSigningRequestList contains a list of ManifestSigningRequest
type ManifestSigningRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ManifestSigningRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ManifestSigningRequest{}, &ManifestSigningRequestList{})
}
