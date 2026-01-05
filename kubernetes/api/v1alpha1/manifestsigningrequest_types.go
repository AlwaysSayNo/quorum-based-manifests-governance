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
	InProgress SigningRequestStatus = "In Progress"
	Error      SigningRequestStatus = "Error"
	Approved   SigningRequestStatus = "Approved"
)

type MSRActionState string

const (
	MSRActionStateEmpty                     MSRActionState = ""
	MSRActionStateGitPushMSR                MSRActionState = "MSRActionStateGitPushMSR"
	MSRActionStateGovernorQubmangoSignature MSRActionState = "MSRActionStateGovernorQubmangoSignature"
	MSRActionStateUpdateAfterGitPush        MSRActionState = "MSRActionStateUpdateAfterGitPush"
	MSRActionStateInitSetFinalizer          MSRActionState = "MSRActionStateInitSetFinalizer"

	// Deletion states
	MSRActionStateDeletion MSRActionState = "MSRActionStateDeletion"

	// Reconcile new version of MSR
	MSRActionStateNewMSRSpec MSRActionState = "MSRActionStateNewMSRSpec"

	// Check fulfillment of MSR rules
	MSRActionStateMSRRulesFulfillment MSRActionState = "MSRActionStateMSRRulesFulfillment"
)

type MSRReconcileNewMSRSpecState string

const (
	MSRReconcileNewMSRSpecStateEmpty              MSRReconcileNewMSRSpecState = ""
	MSRReconcileNewMSRSpecStateGitPushMSR         MSRReconcileNewMSRSpecState = "MSRReconcileNewMSRSpecStateGitPushMSR"
	MSRReconcileNewMSRSpecStateUpdateAfterGitPush MSRReconcileNewMSRSpecState = "MSRReconcileNewMSRSpecStateUpdateAfterGitPush"
	MSRReconcileNewMSRSpecStateNotifyGovernors    MSRReconcileNewMSRSpecState = "MSRReconcileNewMSRSpecStateNotifyGovernors"
)

type MSRRulesFulfillmentState string

const (
	MSRRulesFulfillmentStateEmpty           MSRRulesFulfillmentState = ""
	MSRRulesFulfillmentStateCheckSignatures MSRRulesFulfillmentState = "MSRRulesFulfillmentStateCheckSignatures"
	MSRRulesFulfillmentStateUpdateMCASpec   MSRRulesFulfillmentState = "MSRRulesFulfillmentStateUpdateMCASpec"
	MSRRulesFulfillmentStateNotifyGovernors MSRRulesFulfillmentState = "MSRRulesFulfillmentStateNotifyGovernors"
	MSRRulesFulfillmentStateAbort           MSRRulesFulfillmentState = "MSRRulesFulfillmentStateAbort"
)

type Signature struct {
	// +required
	Signer string `json:"signer" yaml:"signer"`
}

type Locations struct {
	// GovernancePath where MSR, MCA and Signatures will be stored.
	// Default: root of the repo.
	// +kubebuilder:validation:MinLength=1
	// +optional
	GovernancePath string `json:"governancePath" yaml:"governancePath"`
	// +optional
	SourcePath string `json:"sourcePath" yaml:"sourcePath"`
}

type VersionedManifestRef struct {
	// +required
	Name string `json:"name" yaml:"name"`
	// +required
	Namespace string `json:"namespace" yaml:"namespace"`

	// +required
	Version int `json:"version" yaml:"version"`
}

type FileChange struct {
	// +required
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	// +required
	Status FileChangeStatus `json:"status,omitempty" yaml:"status,omitempty"`
	// +required
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// +required
	Namespace string `json:"namespace" yaml:"namespace"`
	// +required
	SHA256 string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	// +required
	Path string `json:"path" yaml:"path"`
}

type ApproverList struct {
	// List of governors.
	// +optional
	Members []Governor `json:"members" yaml:"members"`
}

type ManifestSigningRequestHistoryRecord struct {
	// +required
	CommitSHA string `json:"commitSHA" yaml:"commitSHA"`

	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// +required
	Version int `json:"version" yaml:"version"`

	// +required
	Changes []FileChange `json:"changes" yaml:"changes"`

	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// +required
	Require ApprovalRule `json:"require" yaml:"require"`

	// +optional
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`

	// +optional
	Status SigningRequestStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

// ManifestSigningRequestSpec defines the desired state of ManifestSigningRequest
type ManifestSigningRequestSpec struct {
	// 0 is value of the default ManifestSigningRequest, created by qubmango
	// +kubebuilder:validation:Minimum=0
	// +required
	Version int `json:"version" yaml:"version"`

	// +required
	CommitSHA string `json:"commitSha" yaml:"commitSha"`

	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// +optional
	MRT VersionedManifestRef `json:"mrt" yaml:"mrt"`

	// publicKey is used to sign MCA.
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`

	// +required
	GitRepository GitRepository `json:"gitRepository" yaml:"gitRepository"`

	// Location contains information about where to store MSR, MCA and signatures.
	// +required
	Locations Locations `json:"locations,omitempty" yaml:"locations,omitempty"`

	// +required
	Changes []FileChange `json:"changes" yaml:"changes"`

	// Required until GovernorsRef is implemented.
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// The policy rules for approvals.
	// Required until ApprovalRuleRef is implemented.
	// +required
	Require ApprovalRule `json:"require" yaml:"require"`
}

// ManifestSigningRequestStatus defines the observed state of ManifestSigningRequest.
type ManifestSigningRequestStatus struct {

	// +optional
	RequestHistory []ManifestSigningRequestHistoryRecord `json:"requestHistory,omitempty" yaml:"requestHistory,omitempty"`

	// +optional
	Status SigningRequestStatus `json:"status,omitempty" yaml:"status,omitempty"`

	// +optional
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`

	// +optional
	ActionState MSRActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// +optional
	ReconcileState MSRReconcileNewMSRSpecState `json:"reconcileState,omitempty" yaml:"reconcileState,omitempty"`

	// +optional
	RulesFulfillmentState MSRRulesFulfillmentState `json:"rulesFulfillmentState,omitempty" yaml:"rulesFulfillmentState,omitempty"`

	// conditions represent the current state of the ManifestSigningRequest resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManifestSigningRequest is the Schema for the manifestsigningrequests API
type ManifestSigningRequest struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero" yaml:"metadata"`

	// spec defines the desired state of ManifestSigningRequest
	// +required
	Spec ManifestSigningRequestSpec `json:"spec" yaml:"spec"`

	// status defines the observed state of ManifestSigningRequest
	// +optional
	Status ManifestSigningRequestStatus `json:"status,omitzero" yaml:"status"`
}

type ManifestSigningRequestManifestObject struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef                `json:"metadata,omitzero" yaml:"metadata"`
	Spec            ManifestSigningRequestSpec `json:"spec" yaml:"spec"`
}

// +kubebuilder:object:root=true

// ManifestSigningRequestList contains a list of ManifestSigningRequest
type ManifestSigningRequestList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitzero" yaml:"metadata"`
	Items           []ManifestSigningRequest `json:"items" yaml:"items"`
}

func init() {
	SchemeBuilder.Register(&ManifestSigningRequest{}, &ManifestSigningRequestList{})
}
