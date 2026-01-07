package dto

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type GitRepository struct {
	SSHURL string `json:"sshUrl,omitempty" yaml:"sshUrl,omitempty"`
}

type Locations struct {
	GovernancePath string `json:"governancePath" yaml:"governancePath"`
	SourcePath     string `json:"sourcePath" yaml:"sourcePath"`
}

type SlackChannel struct {
	ChannelID string `json:"channelID,omitempty" yaml:"channelID,omitempty"`
}

type NotificationChannel struct {
	Slack *SlackChannel `json:"slack,omitempty" yaml:"slack,omitempty"`
}

type Governor struct {
	Alias     string `json:"alias,omitempty" yaml:"alias,omitempty"`
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
}

type GovernorList struct {
	NotificationChannels []NotificationChannel `json:"notificationChannels,omitempty" yaml:"notificationChannels,omitempty"`
	Members              []Governor            `json:"members,omitempty" yaml:"members,omitempty"`
}

type ApproverList struct {
	Members []Governor `json:"members" yaml:"members"`
}

type ApprovalRule struct {
	AtLeast *int           `json:"atLeast,omitempty" yaml:"atLeast,omitempty"`
	All     *bool          `json:"all,omitempty" yaml:"all,omitempty"`
	Require []ApprovalRule `json:"require,omitempty" yaml:"require,omitempty"`
	Signer  string         `json:"signer,omitempty" yaml:"signer,omitempty"`
}

type ManifestSigningRequestHistoryRecord struct {
	Version   int          `json:"version" yaml:"version"`
	Changes   []FileChange `json:"changes" yaml:"changes"`
	Governors GovernorList `json:"governors" yaml:"governors"`
	Require   ApprovalRule `json:"require" yaml:"require"`
	Approves  ApproverList `json:"approves" yaml:"approves"`
}

type ManifestSigningRequestSpec struct {
	Version           int                  `json:"version" yaml:"version"`
	CommitSHA         string               `json:"commitSha" yaml:"commitSha"`
	PreviousCommitSHA string               `json:"previousCommitSha" yaml:"previousCommitSha"`
	MRT               VersionedManifestRef `json:"mrt" yaml:"mrt"`
	PublicKey         string               `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	GitRepository     GitRepository        `json:"gitRepository" yaml:"gitRepository"`
	Locations         Locations            `json:"locations" yaml:"locations"`
	Changes           []FileChange         `json:"changes" yaml:"changes"`
	Governors         GovernorList         `json:"governors" yaml:"governors"`
	Require           ApprovalRule         `json:"require" yaml:"require"`
}

type ManifestSigningRequestManifestObject struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef                `json:"metadata" yaml:"metadata"`
	Spec            ManifestSigningRequestSpec `json:"spec" yaml:"spec"`
}
