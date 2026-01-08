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

// Define the states as constants for type safety
type MRTActionState string

const (
	// MRTActionStateEmpty indicates a free state.
	MRTActionStateEmpty MRTActionState = ""
	// MRTActionStateGitGovernanceInitialization indicates the controller is pushing the initial commit.
	MRTActionStateGitGovernanceInitialization MRTActionState = "MRTActionStateGitGovernanceInitialization"
	// MRTActionStateSaveArgoCDTargetRevision indicates the controller is saving initial Application targetRevision.
	MRTActionStateSaveArgoCDTargetRevision MRTActionState = "MRTActionStateSaveArgoCDTargetRevision"
	// MRTActionStateCreateDefaultClusterResources indicates the controller is creating the MSR/MCA.
	MRTActionStateCreateDefaultClusterResources MRTActionState = "MRTActionStateCreateDefaultClusterResources"
	// MRTActionStateInitSetFinalizer indicates the controller is adding the finalizer.
	MRTActionStateInitSetFinalizer MRTActionState = "MRTActionStateInitSetFinalizer"

	// Deletion states
	MRTActionStateDeletion MRTActionState = "MRTActionStateDeletion"

	// Normal operation states
	MRTActionStateCheckingDependencies MRTActionState = "MRTActionStateCheckingDependencies"
	MRTActionStateNewRevision          MRTActionState = "MRTActionStateNewRevision"
)

type MRTNewRevisionState string

const (
	MRTNewRevisionStateEmpty          MRTNewRevisionState = ""
	MRTNewRevisionStatePreflightCheck MRTNewRevisionState = "MRTNewRevisionStatePreflightCheck"
	MRTNewRevisionStateUpdateMSRSpec  MRTNewRevisionState = "MRTNewRevisionStateUpdateMSRSpec"
	MRTNewRevisionStateAfterMSRUpdate MRTNewRevisionState = "MRTNewRevisionStateAfterMSRUpdate"
	MRTNewRevisionStateAbort          MRTNewRevisionState = "MRTNewRevisionStateAbort"
)

type GitRepository struct {
	// +required
	SSHURL string `json:"sshUrl,omitempty" yaml:"sshUrl,omitempty"`
}

type ManifestRef struct {
	// +required
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// +required
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

type ManifestRefOptional struct {
	// +optional
	Name string `json:"name" yaml:"name"`
	// +optional
	Namespace string `json:"namespace" yaml:"namespace"`
}

type ArgoCDApplication struct {
	// Name of the ArgoCD Application.
	// It should contain information about the git repository, branch and path where manifests are stored.
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Namespace where the ArgoCD Application is located.
	// Default is "argocd".
	// +kubebuilder:validation:MinLength=0
	// +optional
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

type MRTLocations struct {
	// GovernancePath where MSR, MCA and Signatures will be stored.
	// Default: root of the repo.
	// +kubebuilder:validation:MinLength=1
	// +optional
	GovernancePath string `json:"governancePath,omitempty" yaml:"governancePath,omitempty"`
}

type MCA struct {
	// Name of the MCA resource.
	// Default is "mca".
	// +kubebuilder:validation:MinLength=1
	// +optional
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Namespace where the MCA resource will be created.
	// Default is the same namespace as the MRT.
	// +kubebuilder:validation:MinLength=0
	// +optional
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

type SlackChannel struct {
	// Slack channel ID to notify (e.g., S01234567)
	// +kubebuilder:validation:MinLength=1
	// +required
	ChannelID string `json:"channelID,omitempty" yaml:"channelID,omitempty"`
}

type NotificationChannel struct {

	// +optional
	Slack *SlackChannel `json:"slack,omitempty" yaml:"slack,omitempty"`

	// Other notification channels can be added in the future
	// ...

}

type Governor struct {
	// Alias for governor (for easier identification)
	// +kubebuilder:validation:MinLength=1
	// +required
	Alias string `json:"alias,omitempty" yaml:"alias,omitempty"`

	// PublicKey of the governor used to verify signatures.
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
}

type GovernorList struct {
	// General list of notification channels to inform governors about pending approvals.
	// +optional
	NotificationChannels []NotificationChannel `json:"notificationChannels,omitempty" yaml:"notificationChannels,omitempty"`

	// List of governors.
	// +required
	Members []Governor `json:"members,omitempty" yaml:"members,omitempty"`
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
	// +kubebuilder:validation:Minimum=1
	// +optional
	AtLeast *int `json:"atLeast,omitempty" yaml:"atLeast,omitempty"`

	// If true, all child rules must be satisfied.
	// +optional
	All *bool `json:"all,omitempty" yaml:"all,omitempty"`

	// List of child rules.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Require []ApprovalRule `json:"require,omitempty" yaml:"require,omitempty"`

	// Signer can be either PublicKey or Alias of the governor. If alias is used, it should start with `$` to distinguish it from PublicKey.
	// +kubebuilder:validation:MinLength=1
	// +optional
	Signer string `json:"signer,omitempty" yaml:"signer,omitempty"`
}

type PGPPrivateKeySecret struct {
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	// +required
	SecretsRef ManifestRef `json:"secretsRef,omitempty" yaml:"secretsRef,omitempty"`
}

type SSHPrivateKeySecret struct {
	// +required
	SecretsRef ManifestRef `json:"secretsRef,omitempty" yaml:"secretsRef,omitempty"`
}

// ManifestRequestTemplateSpec defines the desired state of ManifestRequestTemplate
type ManifestRequestTemplateSpec struct {

	// Version is the current version of the ManifestRequestTemplate.
	// Each new MRT must have a version higher than the previous one.
	// +kubebuilder:validation:Minimum=1
	// +required
	Version int `json:"version" yaml:"version"`

	// +require
	GitRepository GitRepository `json:"gitRepository" yaml:"gitRepository"`

	// +required
	PGP *PGPPrivateKeySecret `json:"pgp" yaml:"pgp"`

	// +required
	SSH *SSHPrivateKeySecret `json:"ssh" yaml:"ssh"`

	// ArgoCDApplicationName is the name of the ArgoCD Application.
	// It should contain information about the git repository, branch and path where manifests are stored.
	// +required
	ArgoCDApplication ArgoCDApplication `json:"argoCDApplication,omitempty" yaml:"argoCDApplication,omitempty"`

	// Locations contains information about where to store MSR, MCA and signatures.
	// +optional
	Locations MRTLocations `json:"locations,omitempty" yaml:"locations,omitempty"`

	// MSR contains information about MSR metadata.
	// +optional
	MSR ManifestRefOptional `json:"msr,omitempty" yaml:"msr,omitempty"`

	// MCA contains information about MCA metadata.
	// +optional
	MCA ManifestRefOptional `json:"mca,omitempty" yaml:"mca,omitempty"`

	// Required until GovernorsRef is implemented.
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// The policy rules for approvals.
	// Required until ApprovalRuleRef is implemented.
	// +required
	Require ApprovalRule `json:"require" yaml:"require"`
}

// ManifestRequestTemplateStatus defines the observed state of ManifestRequestTemplate.
type ManifestRequestTemplateStatus struct {

	// conditions represent the current state of the ManifestRequestTemplate resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`

	// RevisionsQueueRef is the reference to the dedicated queue CRD.
	// +optional
	RevisionQueueRef ManifestRefOptional `json:"revisionQueueRef,omitempty" yaml:"revisionQueueRef,omitempty"`

	// LastMSRVersion is the version of the last created MSR resource
	LastMSRVersion int `json:"lastMSR,omitempty" yaml:"lastMSR,omitempty"`

	// LastAcceptedMSRVersion is the version of the last accepted MSR resource
	LastAcceptedMSRVersion int `json:"lastAcceptedMSR,omitempty" yaml:"lastAcceptedMSR,omitempty"`

	// ActionState tracks the progress of the main reconcile.
	// +optional
	ActionState MRTActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// RevisionProcessingState tracks the progress of the revision processing.
	// +optional
	RevisionProcessingState MRTNewRevisionState `json:"revisionProcessingStep,omitempty" yaml:"revisionProcessingStep,omitempty"`

	ApplicationInitTargetRevision string `json:"applicationInitTargetRevision,omitempty" yaml:"applicationInitTargetRevision,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManifestRequestTemplate is the Schema for the manifestrequesttemplates API
type ManifestRequestTemplate struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero" yaml:"metadata"`

	// spec defines the desired state of ManifestRequestTemplate
	// +required
	Spec ManifestRequestTemplateSpec `json:"spec" yaml:"spec"`

	// status defines the observed state of ManifestRequestTemplate
	// +optional
	Status ManifestRequestTemplateStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ManifestRequestTemplateList contains a list of ManifestRequestTemplate
type ManifestRequestTemplateList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Items           []ManifestRequestTemplate `json:"items" yaml:"items"`
}

const (
	Available   = "Available"
	Progressing = "Progressing"
	Degraded    = "Degraded"
)

func init() {
	SchemeBuilder.Register(&ManifestRequestTemplate{}, &ManifestRequestTemplateList{})
}
