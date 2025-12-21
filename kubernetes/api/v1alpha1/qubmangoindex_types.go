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

type QubmangoPolicy struct {
	// +required
	Alias string `json:"alias,omitempty"`
	// +required
	GovernancePath string `json:"governancePath,omitempty"`
}

// QubmangoIndexSpec defines the desired state of QubmangoIndex
type QubmangoIndexSpec struct {
	// +optional
	Policies []QubmangoPolicy `json:"policies"`
}

// QubmangoIndexStatus defines the observed state of QubmangoIndex.
type QubmangoIndexStatus struct {
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

// QubmangoIndex is the Schema for the qubmangoindices API
type QubmangoIndex struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of QubmangoIndex
	// +required
	Spec QubmangoIndexSpec `json:"spec"`

	// status defines the observed state of QubmangoIndex
	// +optional
	Status QubmangoIndexStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// QubmangoIndexList contains a list of QubmangoIndex
type QubmangoIndexList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []QubmangoIndex `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QubmangoIndex{}, &QubmangoIndexList{})
}
