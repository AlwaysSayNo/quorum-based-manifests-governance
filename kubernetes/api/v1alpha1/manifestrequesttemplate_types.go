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
	// MRTActionStateSaveArgoCDTargetRevision indicates the controller is saving initial Application targetRevision.
	MRTActionStateSaveArgoCDTargetRevision MRTActionState = "MRTActionStateSaveArgoCDTargetRevision"
	// MRTActionStateCheckGovernancePathEmpty indicates the controller is verifying the governance path is empty.
	MRTActionStateCheckGovernancePathEmpty MRTActionState = "MRTActionStateCheckGovernancePathEmpty"
	// MRTActionStateCreateDefaultClusterResources indicates the controller is creating the MSR/MCA.
	MRTActionStateCreateDefaultClusterResources MRTActionState = "MRTActionStateCreateDefaultClusterResources"
	// MRTActionStateInitSetFinalizer indicates the controller is adding the finalizer.
	MRTActionStateInitSetFinalizer MRTActionState = "MRTActionStateInitSetFinalizer"

	// Deletion states
	MRTActionStateDeleteRestoreArgoCD          MRTActionState = "MRTActionStateDeleteRestoreArgoCD"
	MRTActionStateDeleteRemoveGovernanceFolder MRTActionState = "MRTActionStateDeleteRemoveGovernanceFolder"
	MRTActionStateDeleteRemoveFinalizer        MRTActionState = "MRTActionStateDeleteRemoveFinalizer"

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

type GitSSH struct {
	// +required
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
	// +required
	SecretsRef *ManifestRef `json:"secretsRef,omitempty" yaml:"secretsRef,omitempty"`
}

type GitRepository struct {
	// +required
	SSH GitSSH `json:"ssh" yaml:"ssh"`
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

type ArgoCD struct {
	Application ManifestRef `json:"application" yaml:"application"`
}

type SlackSecret struct {
	// SecretsRef points to the Secret containing the 'token' key for the Slack Bot.
	// +required
	SecretsRef ManifestRef `json:"secretsRef" yaml:"secretsRef"`
}

// NotificationConfig holds the configuration for different notification providers.
type NotificationConfig struct {
	// Slack contains the configuration for Slack notifications, including the token secret.
	// If this field is omitted, Slack notifications will be disabled.
	// +optional
	Slack *SlackSecret `json:"slack,omitempty" yaml:"slack,omitempty"`
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

	// +optional
	Notifications *NotificationConfig `json:"notifications,omitempty" yaml:"notifications,omitempty"`

	// ArgoCD contains information about the git repository, branch and path where manifests are stored.
	// +required
	ArgoCD ArgoCD `json:"argoCD,omitempty" yaml:"argoCD,omitempty"`

	// MSR contains information about MSR metadata.
	// +required
	MSR ManifestRef `json:"msr,omitempty" yaml:"msr,omitempty"`

	// MCA contains information about MCA metadata.
	// +required
	MCA ManifestRef `json:"mca,omitempty" yaml:"mca,omitempty"`

	// +optional
	GovernanceFolderPath string `json:"governanceFolderPath" yaml:"governanceFolderPath"`

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

	// RevisionsQueueRef is the reference to the dedicated queue CRD.
	// +optional
	RevisionQueueRef ManifestRefOptional `json:"revisionQueueRef,omitempty" yaml:"revisionQueueRef,omitempty"`

	// LastObservedCommitHash is the last observed commit hash from the git repository
	LastObservedCommitHash string `json:"lastObservedCommitHash,omitempty" yaml:"lastObservedCommitHash,omitempty"`

	// ActionState tracks the progress of the main reconcile.
	// +optional
	ActionState MRTActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// RevisionProcessingState tracks the progress of the revision processing.
	// +optional
	RevisionProcessingState MRTNewRevisionState `json:"revisionProcessingStep,omitempty" yaml:"revisionProcessingStep,omitempty"`

	ApplicationInitTargetRevision string `json:"applicationInitTargetRevision,omitempty" yaml:"applicationInitTargetRevision,omitempty"`

	// conditions represent the current state of the ManifestRequestTemplate resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
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
