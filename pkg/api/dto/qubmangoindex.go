package dto

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type QubmangoPolicy struct {
	Alias          string      `json:"alias,omitempty" yaml:"alias,omitempty"`
	GovernancePath string      `json:"governancePath,omitempty" yaml:"governancePath,omitempty"`
	MSR            ManifestRef `json:"msr" yaml:"msr"`
	MCA            ManifestRef `json:"mca" yaml:"mca"`
}

type QubmangoIndexSpec struct {
	Policies []QubmangoPolicy `json:"policies" yaml:"policies"`
}

type QubmangoIndex struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef       `json:"metadata" yaml:"metadata"`
	Spec            QubmangoIndexSpec `json:"spec" yaml:"spec"`
}
