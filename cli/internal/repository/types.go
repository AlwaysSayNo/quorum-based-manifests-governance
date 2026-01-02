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

type PathStatusGetter interface {
	GetPath() string
	GetStatus() string
}

type FileBytesWithStatus struct {
	Content []byte
	Status  FileChangeStatus
	Path    string
}

func (f FileBytesWithStatus) GetPath() string   { return f.Path }
func (f FileBytesWithStatus) GetStatus() string { return string(f.Status) }

type FileChange struct {
	Kind      string           `json:"kind,omitempty" yaml:"kind,omitempty"`
	Status    FileChangeStatus `json:"status,omitempty" yaml:"status,omitempty"`
	Name      string           `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace string           `json:"namespace" yaml:"namespace"`
	SHA256    string           `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	Path      string           `json:"path" yaml:"path"`
}

func (f FileChange) GetPath() string   { return f.Path }
func (f FileChange) GetStatus() string { return string(f.Status) }

type SigningRequestStatus string

const (
	InProgress  SigningRequestStatus = "In Progress"
	NotApproved SigningRequestStatus = "Not Approved"
	Approved    SigningRequestStatus = "Approved"
)

type ManifestRef struct {
	Name      string `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

type VersionedManifestRef struct {
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Version   int    `json:"version" yaml:"version"`
}

type GitRepository struct {
	SSHURL string `json:"sshUrl,omitempty" yaml:"sshUrl,omitempty"`
}

type Location struct {
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
	Version   int                  `json:"version" yaml:"version"`
	Changes   []FileChange         `json:"changes" yaml:"changes"`
	Governors GovernorList         `json:"governors,omitempty" yaml:"governors,omitempty"`
	Require   ApprovalRule         `json:"require" yaml:"require"`
	Approves  ApproverList         `json:"approves" yaml:"approves"`
	Status    SigningRequestStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type ManifestSigningRequestSpec struct {
	Version           int                  `json:"version" yaml:"version"`
	CommitSHA         string               `json:"commitSha" yaml:"commitSha"`
	PreviousCommitSHA string               `json:"previousCommitSha" yaml:"previousCommitSha"`
	MRT               VersionedManifestRef `json:"mrt" yaml:"mrt"`
	PublicKey         string               `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	GitRepository     GitRepository        `json:"gitRepository" yaml:"gitRepository"`
	Location          Location             `json:"locations,omitempty" yaml:"locations,omitempty"`
	Changes           []FileChange         `json:"changes" yaml:"changes"`
	Governors         GovernorList         `json:"governors,omitempty" yaml:"governors,omitempty"`
	Require           ApprovalRule         `json:"require" yaml:"require"`
	Status            SigningRequestStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type ManifestSigningRequestManifestObject struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef                `json:"metadata,omitzero" yaml:"metadata"`
	Spec            ManifestSigningRequestSpec `json:"spec" yaml:"spec"`
}

type QubmangoPolicy struct {
	Alias          string      `json:"alias,omitempty" yaml:"alias,omitempty"`
	GovernancePath string      `json:"governancePath,omitempty" yaml:"governancePath,omitempty"`
	MSR            ManifestRef `json:"msr,omitempty" yaml:"msr,omitempty"`
	MCA            ManifestRef `json:"mca,omitempty" yaml:"mca,omitempty"`
}

type QubmangoIndexSpec struct {
	Policies []QubmangoPolicy `json:"policies" yaml:"policies"`
}

type QubmangoIndex struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	ObjectMeta      ManifestRef       `json:"metadata,omitzero" yaml:"metadata"`
	Spec            QubmangoIndexSpec `json:"spec" yaml:"spec"`
}
