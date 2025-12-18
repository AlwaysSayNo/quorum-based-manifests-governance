package repository

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FileChangeStatus string

const (
	New     FileChangeStatus = "New"
	Updated FileChangeStatus = "Updated"
	Deleted FileChangeStatus = "Deleted"
)

type FileChange struct {
	Kind      string           `json:"kind,omitempty"`
	Status    FileChangeStatus `json:"status,omitempty"`
	Name      string           `json:"name,omitempty"`
	Namespace string           `json:"namespace"`
	SHA256    string           `json:"sha256,omitempty"`
	Path      string           `json:"path"`
}

type SigningRequestStatus string

const (
	InProgress  SigningRequestStatus = "In Progress"
	NotApproved SigningRequestStatus = "Not Approved"
	Approved    SigningRequestStatus = "Approved"
)

type ManifestRef struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type VersionedManifestRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
}

type Location struct {
	Folder string `json:"folder,omitempty"`
}

type SlackChannel struct {
	ChannelID string `json:"channelID,omitempty"`
}

type NotificationChannel struct {
	Slack *SlackChannel `json:"slack,omitempty"`
}

type Governor struct {
	Alias     string `json:"alias,omitempty"`
	PublicKey string `json:"publicKey,omitempty"`
}

type GovernorList struct {
	NotificationChannels []NotificationChannel `json:"notificationChannels,omitempty"`
	Members              []Governor            `json:"members,omitempty"`
}

type ApproverList struct {
	Members []Governor `json:"members"`
}

type ApprovalRule struct {
	AtLeast *int           `json:"atLeast,omitempty"`
	All     *bool          `json:"all,omitempty"`
	Require []ApprovalRule `json:"require,omitempty"`
	Signer  string         `json:"signer,omitempty"`
}

type ManifestSigningRequestHistoryRecord struct {
	Version   int                  `json:"version"`
	Changes   []FileChange         `json:"changes"`
	Governors GovernorList         `json:"governors,omitempty"`
	Require   ApprovalRule         `json:"require"`
	Approves  ApproverList         `json:"approves"`
	Status    SigningRequestStatus `json:"status,omitempty"`
}

type ManifestSigningRequestSpec struct {
	Version       int                  `json:"version"`
	MRT           VersionedManifestRef `json:"mrt"`
	PublicKey     string               `json:"publicKey,omitempty"`
	GitRepository GitRepository        `json:"gitRepository"`
	Location      Location             `json:"location,omitempty"`
	Changes       []FileChange         `json:"changes"`
	Governors     GovernorList         `json:"governors,omitempty"`
	Require       ApprovalRule         `json:"require"`
	Status        SigningRequestStatus `json:"status,omitempty"`
}

type ManifestSigningRequestManifestObject struct {
	metav1.TypeMeta `json:",inline"`
	ObjectMeta      ManifestRef                `json:"metadata,omitzero"`
	Spec            ManifestSigningRequestSpec `json:"spec"`
}
