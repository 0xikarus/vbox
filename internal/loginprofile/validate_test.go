package loginprofile

import (
	"testing"
	"time"
)

func TestValidatePortableCredentials(t *testing.T) {
	for _, tc := range []struct {
		app, file, data string
		valid           bool
	}{
		{"claude", ".credentials.json", `{"claudeAiOauth":{"accessToken":"test","expiresAt":4102444800000}}`, true},
		{"claude", ".credentials.json", `{"claudeAiOauth":{"expiresAt":1}}`, false},
		{"claude", ".credentials.json", `{"claudeAiOauth":{"accessToken":"test","expiresAt":1}}`, false},
		{"codex", "auth.json", `{"OPENAI_API_KEY":"test"}`, true},
		{"codex", "auth.json", `{"tokens":{"access_token":"invalid"}}`, false},
		{"github", "credential.json", `{"host":"github.com","user":"test-user","token":"test"}`, true},
		{"github", "credential.json", `{"host":"github.com\nother","user":"test","token":"test"}`, false},
		{"github", "credential.json", `{"host":"github.com","user":"test","token":""}`, false},
	} {
		t.Run(tc.app+tc.data, func(t *testing.T) {
			err := Validate(tc.app, map[string][]byte{tc.file: []byte(tc.data)}, time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
}
