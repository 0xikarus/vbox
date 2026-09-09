package v1

import "strings"

// CreateLogicalBoxRequest creates a persistent workspace independently from
// the warm compute fleet. VolumeID/VolumeName are only used when an owner
// explicitly imports an existing detached volume; ordinary creation leaves
// both empty and lets the controller create and detach the volume safely.
type CreateLogicalBoxRequest struct {
	SetupScript          string            `json:"setupScript,omitempty"`
	Tools                []string          `json:"tools,omitempty"`
	LoginProfiles        []LoginProfileRef `json:"loginProfiles,omitempty"`
	Name                 string            `json:"name"`
	Provider             string            `json:"provider"`
	ProviderCredential   string            `json:"providerCredential,omitempty"`
	DefaultAgent         string            `json:"defaultAgent,omitempty"`
	Region               string            `json:"region,omitempty"`
	DiskGiB              int64             `json:"diskGiB,omitempty"`
	VolumeID             string            `json:"volumeId,omitempty"`
	VolumeName           string            `json:"volumeName,omitempty"`
	AllocateWhenReady    bool              `json:"allocateWhenReady,omitempty"`
	AllocationRequestKey string            `json:"allocationIdempotencyKey,omitempty"`
}

func (r *CreateLogicalBoxRequest) Normalize() {
	r.DefaultAgent = strings.ToLower(strings.TrimSpace(r.DefaultAgent))
	if r.DefaultAgent == "" {
		r.DefaultAgent = "claude"
	}
	if r.DiskGiB <= 0 {
		r.DiskGiB = 10
	}
}

type UpdateLogicalBoxRequest struct {
	DefaultAgent string `json:"defaultAgent"`
}
