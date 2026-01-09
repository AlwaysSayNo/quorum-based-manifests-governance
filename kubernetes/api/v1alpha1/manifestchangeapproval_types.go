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

type MCAActionState string

const (
	MCAActionStateEmpty              MCAActionState = ""
	MCAActionStateGitPushMCA         MCAActionState = "MCAActionStateGitPushMCA"
	MCAActionStatePushSummaryFiles   MCAActionState = "MCAActionStatePushSummaryFiles"
	MCAActionStateUpdateAfterGitPush MCAActionState = "MCAActionStateUpdateAfterGitPush"
	MCAActionStateUpdateArgoCD       MCAActionState = "MCAActionStateUpdateArgoCD"
	MCAActionStateInitSetFinalizer   MCAActionState = "MCAActionStateInitSetFinalizer"

	// Deletion states
	MCAActionStateDeletion MCAActionState = "MCAActionStateDeletion"

	//
	MCAActionStateNewMCASpec MCAActionState = "MCAActionStateNewMCASpec"
)

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
	// 0 is value of the default ManifestSigningRequest, created by qubmango
	// +kubebuilder:validation:Minimum=0
	// +required
	Version int `json:"version" yaml:"version"`

	// +required
	CommitSHA string `json:"commitSha" yaml:"commitSha"`

	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// +required
	MRT VersionedManifestRef `json:"mrt,omitempty" yaml:"mrt,omitempty"`

	// +required
	MSR VersionedManifestRef `json:"msr,omitempty" yaml:"msr,omitempty"`

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

	// Signers contains all governors, who signed ManifestSigningRequest, related to this approval
	// +optional
	CollectedSignatures []Signature `json:"collectedSignatures,omitempty" yaml:"collectedSignatures,omitempty"`
}

type ManifestChangeApprovalHistoryRecord struct {
	// CommitSHA is the SHA of the approved commit
	// +required
	CommitSHA string `json:"commitSHA" yaml:"commitSHA"`

	// +required
	PreviousCommitSHA string `json:"previousCommitSha" yaml:"previousCommitSha"`

	// Time is the time when the approval was created
	Time metav1.Time `json:"time" yaml:"time"`

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
}

// ManifestChangeApprovalStatus defines the observed state of ManifestChangeApproval.
type ManifestChangeApprovalStatus struct {

	// History of approvals
	// +optional
	ApprovalHistory []ManifestChangeApprovalHistoryRecord `json:"approvalHistory,omitempty" yaml:"approvalHistory,omitempty"`

	// +optional
	ActionState MCAActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// +optional
	ReconcileState MCAReconcileNewMCASpecState `json:"reconcileState,omitempty" yaml:"reconcileState,omitempty"`

	// conditions represent the current state of the ManifestChangeApproval resource.
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
