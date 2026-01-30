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

// EventType defines the type of governance event.
type EventType string

const (
	// EventTypeNewRevision indicates a new Git commit was detected.
	EventTypeNewRevision EventType = "NewRevision"
)

// NewRevisionPayload holds the data for a NewRevision event.
type NewRevisionPayload struct {
	CommitSHA string `json:"commitSHA" yaml:"commitSHA"`
}

// GovernanceEventSpec defines the desired state of GovernanceEvent
type GovernanceEventSpec struct {
	// Type indicates the kind of event this is.
	Type EventType `json:"type" yaml:"type"`

	MRT ManifestRef `json:"mrt,omitempty" yaml:"mrt,omitempty"`

	// NewRevision holds the payload if the event type is NewRevision.
	// +optional
	NewRevision *NewRevisionPayload `json:"newRevision,omitempty" yaml:"newRevision,omitempty"`
}

// GovernanceEventStatus defines the observed state of GovernanceEvent.
type GovernanceEventStatus struct {
	// conditions represent the current state of the GovernanceEvent resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GovernanceEvent is the Schema for the governanceevents API
type GovernanceEvent struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero" yaml:"metadata,omitzero"`

	// spec defines the desired state of GovernanceEvent
	// +required
	Spec GovernanceEventSpec `json:"spec" yaml:"spec"`

	// status defines the observed state of GovernanceEvent
	// +optional
	Status GovernanceEventStatus `json:"status,omitzero" yaml:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GovernanceEventList contains a list of GovernanceEvent
type GovernanceEventList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitzero" yaml:"metadata,omitzero"`
	Items           []GovernanceEvent `json:"items" yaml:"items"`
}

func init() {
	SchemeBuilder.Register(&GovernanceEvent{}, &GovernanceEventList{})
}
