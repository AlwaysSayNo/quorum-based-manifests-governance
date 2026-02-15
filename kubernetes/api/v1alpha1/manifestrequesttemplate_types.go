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

// MRTActionState represents the main states of the MRT reconciliation process, including both initialization and deletion states.
type MRTActionState string

const (
	// MRTActionStateEmpty - a free state. Default value when no action is set.
	MRTActionStateEmpty MRTActionState = ""
	// MRTActionStateSaveArgoCDTargetRevision - the controller is saving initial Application targetRevision.
	MRTActionStateSaveArgoCDTargetRevision MRTActionState = "MRTActionStateSaveArgoCDTargetRevision"
	// MRTActionStateCheckGovernancePathEmpty - the controller is verifying the governance path is empty on initialization.
	MRTActionStateCheckGovernancePathEmpty MRTActionState = "MRTActionStateCheckGovernancePathEmpty"
	// MRTActionStateCreateDefaultClusterResources - the controller is creating the MSR/MCA.
	MRTActionStateCreateDefaultClusterResources MRTActionState = "MRTActionStateCreateDefaultClusterResources"
	// MRTActionStateInitSetFinalizer - the controller is adding the finalizer.
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

type MRTDeletionState string

const (
	MRTDeletionStateEmpty                  MRTDeletionState = ""
	MRTDeletionStateRestoreArgoCD          MRTDeletionState = "RestoreArgoCD"
	MRTDeletionStateRemoveGovernanceFolder MRTDeletionState = "RemoveGovernanceFolder"
	MRTDeletionStateRemoveFinalizer        MRTDeletionState = "RemoveFinalizer"
)

type GitSSH struct {
	// URL is the Git SSH URL for the repository.
	// +required
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
	// SecretsRef is the Kubernetes secret reference containing SSH credentials.
	// It must have:
	// - 'ssh-privateKey' key with the private SSH key for repository access.
	// - 'passphrase' key with the passphrase for the private SSH key, if applicable (optional).
	// +required
	SecretsRef *ManifestRef `json:"secretsRef,omitempty" yaml:"secretsRef,omitempty"`
}

type GitRepository struct {
	// SSH contains the Git repository SSH configuration
	// +required
	SSH GitSSH `json:"ssh" yaml:"ssh"`
}

type ManifestRef struct {
	// Name is the name of the Kubernetes resource
	// +required
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// Namespace is the namespace of the Kubernetes resource
	// +required
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

type ManifestRefOptional struct {
	// Name is the name of the Kubernetes resource
	// +optional
	Name string `json:"name" yaml:"name"`
	// Namespace is the namespace of the Kubernetes resource
	// +optional
	Namespace string `json:"namespace" yaml:"namespace"`
}

type ArgoCD struct {
	// Application is the reference to the ArgoCD Application resource
	Application ManifestRef `json:"application" yaml:"application"`
}

type SlackSecret struct {
	// SecretsRef points to the Secret containing the token for the Slack Bot.
	// It must have:
	// - 'token' key with the Slack Bot token for sending notifications.
	// +required
	SecretsRef ManifestRef `json:"secretsRef" yaml:"secretsRef"`
}

// NotificationConfig holds the configuration for different notification providers.
type NotificationConfig struct {
	// Slack contains the Kubernetes secret reference for Slack token
	// If this field is omitted, Slack notifications will be disabled.
	// +optional
	Slack *SlackSecret `json:"slack,omitempty" yaml:"slack,omitempty"`
}

type SlackChannel struct {
	// ChannelID is the Slack channel ID to notify (e.g., U0A7XASDMR28)
	// +kubebuilder:validation:MinLength=1
	// +required
	ChannelID string `json:"channelID,omitempty" yaml:"channelID,omitempty"`
}

type NotificationChannel struct {

	// Slack contains the Slack notification channel configuration
	// +optional
	Slack *SlackChannel `json:"slack,omitempty" yaml:"slack,omitempty"`

	// Other notification channels can be added in the future
	// ...

}

type Governor struct {
	// Alias is the human-readable name for the governor
	// +kubebuilder:validation:MinLength=1
	// +required
	Alias string `json:"alias,omitempty" yaml:"alias,omitempty"`

	// PublicKey is the PGP public key of the governor used to verify signatures
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
}

type GovernorList struct {
	// NotificationChannels is the list of channels to inform governors about pending approvals
	// +optional
	NotificationChannels []NotificationChannel `json:"notificationChannels,omitempty" yaml:"notificationChannels,omitempty"`

	// Members is the list of governance board members
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
	// AtLeast specifies the minimum number of child rules that must be satisfied
	// +kubebuilder:validation:Minimum=1
	// +optional
	AtLeast *int `json:"atLeast,omitempty" yaml:"atLeast,omitempty"`

	// All specifies whether all child rules must be satisfied
	// +optional
	All *bool `json:"all,omitempty" yaml:"all,omitempty"`

	// Require is the list of nested approval rules
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Require []ApprovalRule `json:"require,omitempty" yaml:"require,omitempty"`

	// Signer is the governor identifier as an alias
	// +kubebuilder:validation:MinLength=1
	// +optional
	Signer string `json:"signer,omitempty" yaml:"signer,omitempty"`
}

type PGPPrivateKeySecret struct {
	// PublicKey is the PGP public key for signature verification
	// +kubebuilder:validation:MinLength=1
	// +required
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	// SecretsRef is the Kubernetes secret reference for the PGP private key
	// It must have:
	// - 'privateKey' key with the PGP private key for signing.
	// - 'passphrase' key with the passphrase for the private key, if applicable (optional).
	// +required
	SecretsRef ManifestRef `json:"secretsRef,omitempty" yaml:"secretsRef,omitempty"`
}

// ManifestRequestTemplateSpec defines the desired state of ManifestRequestTemplate
type ManifestRequestTemplateSpec struct {

	// Version is the incremental version of the policy.
	// Each new MRT must have a version higher than the previous one.
	// +kubebuilder:validation:Minimum=1
	// +required
	Version int `json:"version" yaml:"version"`

	// GitRepository contains the Git repository configuration
	// +require
	GitRepository GitRepository `json:"gitRepository" yaml:"gitRepository"`

	// PGP contains the PGP configuration for signing and verification
	// +required
	PGP *PGPPrivateKeySecret `json:"pgp" yaml:"pgp"`

	// Notifications contains the notification configuration (e.g., Slack)
	// +optional
	Notifications *NotificationConfig `json:"notifications,omitempty" yaml:"notifications,omitempty"`

	// ArgoCD contains the reference to the ArgoCD application
	// +required
	ArgoCD ArgoCD `json:"argoCD,omitempty" yaml:"argoCD,omitempty"`

	// MSR initially contains information that is used to create dependent MSR instance.
	// After creation, it serves as the reference to the instance.
	// +required
	MSR ManifestRef `json:"msr,omitempty" yaml:"msr,omitempty"`

	// MCA initially contains information that is used to create dependent MCA instance.
	// After creation, it serves as the reference to the instance.
	// +required
	MCA ManifestRef `json:"mca,omitempty" yaml:"mca,omitempty"`

	// GovernanceFolderPath is the path in the Git repository, where qubmango will create
	// the operational folder '.qubmango' for its state and governance artifacts (e.g., signatures, MSRs, MCAs).
	// +optional
	GovernanceFolderPath string `json:"governanceFolderPath" yaml:"governanceFolderPath"`

	// Governors contains the governance board configuration.
	// Required until GovernorsRef is implemented.
	// +required
	Governors GovernorList `json:"governors,omitempty" yaml:"governors,omitempty"`

	// Require defines the admission rules for MSR approval.
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
	// +optional
	LastObservedCommitHash string `json:"lastObservedCommitHash,omitempty" yaml:"lastObservedCommitHash,omitempty"`

	// ActionState tracks the progress of the main reconcile state.
	// +optional
	ActionState MRTActionState `json:"actionState,omitempty" yaml:"actionState,omitempty"`

	// RevisionProcessingState tracks the progress of the revision processing sub-state.
	// +optional
	RevisionProcessingState MRTNewRevisionState `json:"revisionProcessingStep,omitempty" yaml:"revisionProcessingStep,omitempty"`

	// DeletionProcessingState tracks the progress of the deletion process sub-state.
	// +optional
	DeletionProcessingState MRTDeletionState `json:"deletionProcessingState,omitempty" yaml:"deletionProcessingState,omitempty"`

	// ApplicationInitTargetRevision is the targetRevision of the ArgoCD Application at the moment of MRT creation.
	// +optional
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
