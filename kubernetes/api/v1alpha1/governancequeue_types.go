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
	"k8s.io/apimachinery/pkg/types"
)

// EventReference holds a reference to a GovernanceEvent object.
type EventReference struct {
	UID       types.UID `json:"uid" yaml:"uid"`
	Name      string    `json:"name" yaml:"name"`
	Namespace string    `json:"namespace" yaml:"namespace"`
}

// GovernanceQueueSpec defines the desired state of GovernanceQueue
type GovernanceQueueSpec struct {
	// +required
	MRT ManifestRef `json:"mrt,omitempty" yaml:"mrt,omitempty"`
}

// GovernanceQueueStatus defines the observed state of GovernanceQueue.
type GovernanceQueueStatus struct {

	// The ordered queue of event references.
	// +optional
	Queue []EventReference `json:"queue,omitempty" yaml:"queue,omitempty"`

	// conditions represent the current state of the GovernanceQueue resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GovernanceQueue is the Schema for the governancequeues API
type GovernanceQueue struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero" yaml:"metadata"`

	// spec defines the desired state of GovernanceQueue
	// +required
	Spec GovernanceQueueSpec `json:"spec" yaml:"spec"`

	// status defines the observed state of GovernanceQueue
	// +optional
	Status GovernanceQueueStatus `json:"status,omitzero" yaml:"status"`
}

// +kubebuilder:object:root=true

// GovernanceQueueList contains a list of GovernanceQueue
type GovernanceQueueList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitzero" yaml:"metadata"`
	Items           []GovernanceQueue `json:"items" yaml:"items"`
}

func init() {
	SchemeBuilder.Register(&GovernanceQueue{}, &GovernanceQueueList{})
}
