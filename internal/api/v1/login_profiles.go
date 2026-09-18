package v1

import "time"

type LoginProfileRef struct {
	Application string `json:"application"`
	Name        string `json:"name"`
	Model       string `json:"model,omitempty"`
}

// LoginProfile is public metadata. Credential bytes never appear in list responses.
type LoginProfile struct {
	Application string    `json:"application"`
	Name        string    `json:"name"`
	Model       string    `json:"model,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type SaveLoginProfileRequest struct {
	Files map[string][]byte `json:"files"`
}

// PutBoxLoginProfilesRequest replaces the login profiles imported into one box.
// An empty list clears the recorded references; it does not remove credential
// files already present in the box, mirroring saved-profile deletion.
type PutBoxLoginProfilesRequest struct {
	Profiles []LoginProfileRef `json:"profiles"`
}

// BoxLoginProfiles reports one box's imported credential references. Pending
// holds a selection queued for the next box start. Credential bytes never
// appear here.
type BoxLoginProfiles struct {
	Imported []LoginProfileRef `json:"profiles"`
	Pending  []LoginProfileRef `json:"pending"`
	Verified bool              `json:"verified"`
	Applied  bool              `json:"applied,omitempty"`
	Note     string            `json:"note,omitempty"`
}
