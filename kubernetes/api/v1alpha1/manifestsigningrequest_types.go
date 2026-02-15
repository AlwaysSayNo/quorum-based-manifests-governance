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

// MSRActionState represents the main states of the MSR reconciliation process, including both initialization and deletion states.
type MSRActionState string

const (
	// Regular states
	// MSRActionStateEmpty represents the initial state before any action has been taken.
	MSRActionStateEmpty MSRActionState = ""
	// MSRActionStateGitPushMSR - push initial MSR state to the Git repository.
	MSRActionStateGitPushMSR MSRActionState = "MSRActionStateGitPushMSR"
	// MSRActionStateGovernorQubmangoSignature - push initial MSR signature made by governance operator to the Git repository.
	MSRActionStateGovernorQubmangoSignature MSRActionState = "MSRActionStateGovernorQubmangoSignature"
	// MSRActionStateUpdateAfterGitPush - update information in-cluster MSR (add history record) after Git repository push.
	MSRActionStateUpdateAfterGitPush MSRActionState = "MSRActionStateUpdateAfterGitPush"
	// MSRActionStateInitSetFinalizer - set finalizer on the MSR resource.
	MSRActionStateInitSetFinalizer MSRActionState = "MSRActionStateInitSetFinalizer"

	// Deletion states
	MSRActionStateDeletion MSRActionState = "MSRActionStateDeletion"

	// Reconcile new version of MSR
	MSRActionStateNewMSRSpec MSRActionState = "MSRActionStateNewMSRSpec"

	// Check fulfillment of MSR rules
	MSRActionStateMSRRulesFulfillment MSRActionState = "MSRActionStateMSRRulesFulfillment"
)

// MSRReconcileNewMSRSpecState represents the sub-states of reconciling a new MSR spec,
// which is a part of the main reconcile flow when a new version of MSR spec is detected.
type MSRReconcileNewMSRSpecState string

const (
	MSRReconcileNewMSRSpecStateEmpty              MSRReconcileNewMSRSpecState = ""
	MSRReconcileNewMSRSpecStateGitPushMSR         MSRReconcileNewMSRSpecState = "MSRReconcileNewMSRSpecStateGitPushMSR"
	MSRReconcileNewMSRSpecStateUpdateAfterGitPush MSRReconcileNewMSRSpecState = "MSRReconcileNewMSRSpecStateUpdateAfterGitPush"
	MSRReconcileNewMSRSpecStateNotifyGovernors    MSRReconcileNewMSRSpecState = "MSRReconcileNewMSRSpecStateNotifyGovernors"
)

// MSRRulesFulfillmentState represents the sub-states of checking approval rules fulfillment,
// which is a part of the main reconcile flow when MSR rules fulfillment needs to be checked.
type MSRRulesFulfillmentState string

const (
	MSRRulesFulfillmentStateEmpty           MSRRulesFulfillmentState = ""
	MSRRulesFulfillmentStateCheckSignatures MSRRulesFulfillmentState = "MSRRulesFulfillmentStateCheckSignatures"
	MSRRulesFulfillmentStateUpdateMCASpec   MSRRulesFulfillmentState = "MSRRulesFulfillmentStateUpdateMCASpec"
	MSRRulesFulfillmentStateNotifyGovernors MSRRulesFulfillmentState = "MSRRulesFulfillmentStateNotifyGovernors"
	MSRRulesFulfillmentStateAbort           MSRRulesFulfillmentState = "MSRRulesFulfillmentStateAbort"
)

type Signature struct {
	// Signer is the identifier of the governor who signed the MSR.
	// It contains governor's alias from the GovernorList.Members board.
	// +required
	Signer string `json:"signer" yaml:"signer"`
}

type Locations struct {
	// GovernancePath is full path in the Git repository (MRT.GovernanceFolderPath + '.qubmango')
	// where governance artifacts (MSR, MCA, signatures etc.) will be stored.
	// Default: root of the repo.
	// +kubebuilder:validation:MinLength=1
	// +optional
	GovernancePath string `json:"governancePath" yaml:"governancePath"`
	// SourcePath is the path in the Git repository to the manifests that are being protected.
	// +optional
	SourcePath string `json:"sourcePath" yaml:"sourcePath"`
}

// VersionedManifestRef used as a reference to governance artifacts inside the cluster
type VersionedManifestRef struct {
	// Name is the name of the Kubernetes resource
	// +required
	Name string `json:"name" yaml:"name"`
	// Namespace is the namespace of the Kubernetes resource
	// +required
	Namespace string `json:"namespace" yaml:"namespace"`
	// Version is the version of the referenced resource
	// +required
	Version int `json:"version" yaml:"version"`
}

type FileChange struct {
	// Kind is the Kubernetes resource kind (e.g., Deployment, Service)
	// +required
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	// Status indicates the change type (New, Updated, or Deleted)
	// +required
	Status FileChangeStatus `json:"status,omitempty" yaml:"status,omitempty"`
	// Name is the name of the Kubernetes resource
	// +required
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// Namespace is the namespace of the Kubernetes resource
	// +required
	Namespace string `json:"namespace" yaml:"namespace"`
	// SHA256 is the hash of the file content.
	// If New or Updated, it represents the new content hash.
	// If Deleted, it represents the last known content hash.
	// +required
	SHA256 string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	// Path is the file path in the Git repository
	// +required
	Path string `json:"path" yaml:"path"`
}

type ApproverList struct {
	// Members is the list of governors who are part of the board responsible for approving the MSR.
	// +optional
	Members []Governor `json:"members" yaml:"members"`
}

type ManifestSigningRequestHistoryRecord struct {
	// CommitSHA is the Git commit hash for this request
	// +required
	CommitSHA string `json:"commitSHA" yaml:"commitSHA"`

	// PreviousCommitSHA is the previous Git commit hash
	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// Version is the version number of this MSR
	// +required
	Version int `json:"version" yaml:"version"`

	// Changes is the list of file changes in this request
	// +required
	Changes []FileChange `json:"changes" yaml:"changes"`

	// Governors is the list of governors for this request
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// Require defines the approval rules for this request
	// +required
	Require ApprovalRule `json:"require" yaml:"require"`

	// CollectedSignatures is the list of signatures collected for this request
	// +optional
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`

	// Status is the current status of the signing request
	// +optional
	Status SigningRequestStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

// ManifestSigningRequestSpec defines the desired state of ManifestSigningRequest
type ManifestSigningRequestSpec struct {
	// Version is the version of this MSR (0 for default MSR created by qubmango)
	// +kubebuilder:validation:Minimum=0
	// +required
	Version int `json:"version" yaml:"version"`

	// CommitSHA is the Git commit hash for this request
	// +required
	CommitSHA string `json:"commitSha" yaml:"commitSha"`

	// PreviousCommitSHA is the previous Git commit hash
	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// MRT is the reference to the parent MRT
	// +required
	MRT VersionedManifestRef `json:"mrt" yaml:"mrt"`

	// PublicKey is the PGP public key of the parent MRT PGP private key.
	// It is used to verify signatures on this MSR.
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`

	// GitRepositoryURL is the SSH URL of the Git repository
	// +required
	GitRepositoryURL string `json:"gitRepositoryUrl" yaml:"gitRepositoryUrl"`

	// Locations container with important paths within the Git repository
	// +required
	Locations Locations `json:"locations,omitempty" yaml:"locations,omitempty"`

	// Changes is the list of file changes in this request
	// +required
	Changes []FileChange `json:"changes" yaml:"changes"`

	// Governors contains the governance board configuration.
	// Required until GovernorsRef is implemented.
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// Require defines the admission rules for MSR approval.
	// Required until ApprovalRuleRef is implemented.
	// +required
	Require ApprovalRule `json:"require" yaml:"require"`
}

// ManifestSigningRequestStatus defines the observed state of ManifestSigningRequest.
type ManifestSigningRequestStatus struct {

	// LastObservedCommitHash is the last observed commit hash from the Git repository
	LastObservedCommitHash string `json:"lastObservedCommitHash,omitempty" yaml:"lastObservedCommitHash,omitempty"`

	// RequestHistory is the history of all signing requests
	// +optional
	RequestHistory []ManifestSigningRequestHistoryRecord `json:"requestHistory,omitempty" yaml:"requestHistory,omitempty"`

	// Status is the current status of the MSR
	// +optional
	Status SigningRequestStatus `json:"status,omitempty" yaml:"status,omitempty"`

	// CollectedSignatures is the list of signatures collected so far
	// +optional
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`

	// ActionState tracks the progress of the main reconcile state
	// +optional
	ActionState MSRActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// ReconcileState tracks the progress of reconciling new MSR spec sub-state
	// +optional
	ReconcileState MSRReconcileNewMSRSpecState `json:"reconcileState,omitempty" yaml:"reconcileState,omitempty"`

	// RulesFulfillmentState tracks the progress of checking approval rules fulfillment sub-state
	// +optional
	RulesFulfillmentState MSRRulesFulfillmentState `json:"rulesFulfillmentState,omitempty" yaml:"rulesFulfillmentState,omitempty"`

	// Conditions represent the current state of the ManifestSigningRequest resource.
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
