package v1

import "time"

type LoginProfileRef struct {
	Application string `json:"application"`
	Name        string `json:"name"`
}

// LoginProfile is public metadata. Credential bytes never appear in list responses.
type LoginProfile struct {
	Application string    `json:"application"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
}

type SaveLoginProfileRequest struct {
	Files map[string][]byte `json:"files"`
}
