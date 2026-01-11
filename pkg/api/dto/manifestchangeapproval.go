package dto

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Signature struct {
	Signer string `json:"signer" yaml:"signer"`
}

type ManifestChangeApprovalSpec struct {
	Version             int                  `json:"version" yaml:"version"`
	CommitSHA           string               `json:"commitSha" yaml:"commitSha"`
	PreviousCommitSHA   string               `json:"previousCommitSha" yaml:"previousCommitSha"`
	MRT                 VersionedManifestRef `json:"mrt" yaml:"mrt"`
	MSR                 VersionedManifestRef `json:"msr" yaml:"msr"`
	PublicKey           string               `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	GitRepositoryURL    string               `json:"gitRepositoryUrl" yaml:"gitRepositoryUrl"`
	Locations           Locations            `json:"locations" yaml:"locations"`
	Changes             []FileChange         `json:"changes" yaml:"changes"`
	Governors           GovernorList         `json:"governors" yaml:"governors"`
	Require             ApprovalRule         `json:"require" yaml:"require"`
	CollectedSignatures []Signature          `json:"collectedSignatures" yaml:"collectedSignatures"`
}

type ManifestChangeApprovalManifestObject struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef                `json:"metadata" yaml:"metadata"`
	Spec            ManifestChangeApprovalSpec `json:"spec" yaml:"spec"`
}
