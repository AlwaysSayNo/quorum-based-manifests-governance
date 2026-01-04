package dto

type SignatureData []byte

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
