package v1

import (
	"encoding/json"
	"time"
)

// BrowserProfile contains owner-visible metadata only. Browser session values
// never appear in list or attach responses.
type BrowserProfile struct {
	Name          string    `json:"name"`
	SourceBrowser string    `json:"sourceBrowser"`
	SourcePath    string    `json:"sourcePath"`
	Origins       []string  `json:"origins"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type SaveBrowserProfileRequest struct {
	SourceBrowser   string          `json:"sourceBrowser"`
	SourcePath      string          `json:"sourcePath"`
	State           json.RawMessage `json:"state"`
	ReplaceExisting bool            `json:"replaceExisting,omitempty"`
}
