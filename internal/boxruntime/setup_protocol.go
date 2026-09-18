package boxruntime

// SyncFile is one atomically replaced file in a SyncRequest. Data is encoded
// as base64 by encoding/json and is never placed in a process argument.
type SyncFile struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Data []byte `json:"data"`
}

type SyncRequest struct {
	Files  []SyncFile `json:"files"`
	Remove []string   `json:"remove,omitempty"`
}

type GitHubSetup struct {
	Host     string `json:"host"`
	User     string `json:"user"`
	Protocol string `json:"protocol"`
	Token    string `json:"token"`
	// Name and Email configure the Git commit identity as the workload user.
	// They are resolved locally from the same account as the token.
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type SetupRequest struct {
	Workspace    string       `json:"workspace"`
	GitHub       *GitHubSetup `json:"github,omitempty"`
	Applications []string     `json:"applications,omitempty"`
}

type SetupResult struct {
	Authentication map[string]bool `json:"authentication,omitempty"`
}
