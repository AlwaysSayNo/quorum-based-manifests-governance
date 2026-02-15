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

// MCAActionState represents the main states of the MCA reconciliation process, including both initialization and deletion states.
type MCAActionState string

const (
	// MSRActionStateEmpty represents the initial state before any action has been taken.
	MCAActionStateEmpty MCAActionState = ""
	// MCAActionStateGitPushMCA - push MCA to the Git repository.
	MCAActionStateGitPushMCA MCAActionState = "MCAActionStateGitPushMCA"
	// MCAActionStatePushSummaryFiles - push summary files to the Git repository.
	MCAActionStatePushSummaryFiles MCAActionState = "MCAActionStatePushSummaryFiles"
	// MCAActionStateUpdateAfterGitPush - update information in-cluster MCA (add history record) after Git repository push.
	MCAActionStateUpdateAfterGitPush MCAActionState = "MCAActionStateUpdateAfterGitPush"
	// MCAActionStateUpdateArgoCD - update ArgoCD to point to the new approved commit.
	MCAActionStateUpdateArgoCD MCAActionState = "MCAActionStateUpdateArgoCD"
	// MCAActionStateInitSetFinalizer - set finalizer on the MCA resource.
	MCAActionStateInitSetFinalizer MCAActionState = "MCAActionStateInitSetFinalizer"

	// Deletion states
	MCAActionStateDeletion MCAActionState = "MCAActionStateDeletion"

	// Reconcile new version of MCA
	MCAActionStateNewMCASpec MCAActionState = "MCAActionStateNewMCASpec"
)

// MCAReconcileNewMCASpecState represents the sub-states of reconciling a new MCA spec,
// which is a part of the main reconcile flow when a new version of MCA spec is detected.
type MCAReconcileNewMCASpecState string

const (
	MCAReconcileNewMCASpecStateEmpty              MCAReconcileNewMCASpecState = ""
	MCAReconcileNewMCASpecStateGitPushMCA         MCAReconcileNewMCASpecState = "MCAReconcileNewMCASpecStateGitPushMCA"
	MCAReconcileNewMCASpecStatePushSummaryFiles   MCAReconcileNewMCASpecState = "MCAReconcileNewMCASpecStatePushSummaryFiles"
	MCAReconcileNewMCASpecStateUpdateArgoCD       MCAReconcileNewMCASpecState = "MCAReconcileNewMCASpecStateUpdateArgoCD"
	MCAReconcileNewMCASpecStateUpdateAfterGitPush MCAReconcileNewMCASpecState = "MCAReconcileNewMCASpecStateUpdateAfterGitPush"
	MCAReconcileNewMCASpecStateNotifyGovernors    MCAReconcileNewMCASpecState = "MCAReconcileNewMCASpecStateNotifyGovernors"
)

// ManifestChangeApprovalSpec defines the desired state of ManifestChangeApproval
type ManifestChangeApprovalSpec struct {
	// Version is the version of this MCA (0 for default MCA created by qubmango)
	// +kubebuilder:validation:Minimum=0
	// +required
	Version int `json:"version" yaml:"version"`

	// CommitSHA is the Git commit hash for this approval
	// +required
	CommitSHA string `json:"commitSha" yaml:"commitSha"`

	// PreviousCommitSHA is the previous Git commit hash
	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// MRT is the reference to the parent MRT
	// +required
	MRT VersionedManifestRef `json:"mrt,omitempty" yaml:"mrt,omitempty"`

	// MSR is the reference to the associated MSR that was approved
	// +required
	MSR VersionedManifestRef `json:"msr,omitempty" yaml:"msr,omitempty"`

	// PublicKey is the PGP public key of the parent MRT PGP private key.
	// It is used to verify signatures on this MCA.
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`

	// GitRepositoryURL is the SSH URL of the Git repository
	// +required
	GitRepositoryURL string `json:"gitRepositoryUrl" yaml:"gitRepositoryUrl"`

	// Locations container with important paths within the Git repository
	// +required
	Locations Locations `json:"locations,omitempty" yaml:"locations,omitempty"`

	// Changes is the list of approved file changes
	// +required
	Changes []FileChange `json:"changes" yaml:"changes"`

	// Governors contains the governance board configuration.
	// Required until GovernorsRef is implemented.
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// Require defines the admission rules that were fulfilled for this approval.
	// Required until ApprovalRuleRef is implemented.
	// +required
	Require ApprovalRule `json:"require" yaml:"require"`

	// CollectedSignatures contains all governors' signatures from the associated MSR
	// that resulted in this MCA.
	// +required
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`
}

type ManifestChangeApprovalHistoryRecord struct {
	// CommitSHA is the Git commit hash of the approved change
	// +required
	CommitSHA string `json:"commitSHA" yaml:"commitSHA"`

	// PreviousCommitSHA is the previous Git commit hash
	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// Time is the timestamp when the approval was created
	Time metav1.Time `json:"time" yaml:"time"`

	// Version is the version number of this MCA
	// +required
	Version int `json:"version" yaml:"version"`

	// Changes is the list of approved file changes
	// +required
	Changes []FileChange `json:"changes" yaml:"changes"`

	// Governors is the list of governors who were part of the board for this approval
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// Require defines the approval rules that for the MSR.
	// +required
	Require ApprovalRule `json:"require" yaml:"require"`

	// CollectedSignatures is the list of signatures that approved this change
	// +optional
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`
}

// ManifestChangeApprovalStatus defines the observed state of ManifestChangeApproval.
type ManifestChangeApprovalStatus struct {

	// ApprovalHistory is the history of all approved changes
	// +optional
	ApprovalHistory []ManifestChangeApprovalHistoryRecord `json:"approvalHistory,omitempty" yaml:"approvalHistory,omitempty"`

	// ActionState tracks the progress of the main reconcile state
	// +optional
	ActionState MCAActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// ReconcileState tracks the progress of reconciling new MCA spec sub-state
	// +optional
	ReconcileState MCAReconcileNewMCASpecState `json:"reconcileState,omitempty" yaml:"reconcileState,omitempty"`

	// Conditions represent the current state of the ManifestChangeApproval resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManifestChangeApproval is the Schema for the manifestchangeapprovals API
type ManifestChangeApproval struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero" yaml:"metadata"`

	// spec defines the desired state of ManifestChangeApproval
	// +required
	Spec ManifestChangeApprovalSpec `json:"spec" yaml:"spec"`

	// status defines the observed state of ManifestChangeApproval
	// +optional
	Status ManifestChangeApprovalStatus `json:"status,omitzero" yaml:"status"`
}

type ManifestChangeApprovalManifestObject struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef                `json:"metadata,omitzero" yaml:"metadata"`
	Spec            ManifestChangeApprovalSpec `json:"spec" yaml:"spec"`
}

// +kubebuilder:object:root=true

// ManifestChangeApprovalList contains a list of ManifestChangeApproval
type ManifestChangeApprovalList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitzero" yaml:"metadata"`
	Items           []ManifestChangeApproval `json:"items" yaml:"items"`
}

func init() {
	SchemeBuilder.Register(&ManifestChangeApproval{}, &ManifestChangeApprovalList{})
}
