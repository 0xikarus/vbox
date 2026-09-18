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
		{"opencode", "auth.json", `{"provider":{"type":"api","key":"synthetic"}}`, true},
		{"opencode", "auth.json", `{"provider":{"type":"oauth","access":"synthetic","refresh":"synthetic","expires":4102444800000}}`, true},
		{"opencode", "auth.json", `{"provider":{"type":"oauth","access":"synthetic","refresh":"synthetic","expires":1}}`, false},
		{"opencode", "auth.json", `{"provider":{"type":"api","key":""}}`, false},
		{"opencode", "auth.json", `{}`, false},
		{"opencode", "auth.json", `null`, false},
		{"claude", ".credentials.json", `{"claudeAiOauth":{"accessToken":"test","expiresAt":4102444800000}}`, true},
		{"claude", ".credentials.json", `{"claudeAiOauth":{"accessToken":"expired","refreshToken":"refreshable","expiresAt":1}}`, true},
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

func TestModelReadsPortableProfileConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, app, want string
		files           map[string][]byte
	}{
		{name: "claude", app: "claude", files: map[string][]byte{"settings.json": []byte(`{"model":"sonnet"}`)}, want: "sonnet"},
		{name: "codex top level", app: "codex", files: map[string][]byte{"config.toml": []byte("model = 'gpt-5.6-sol'\n[projects.x]\nmodel = \"nested\"\n")}, want: "gpt-5.6-sol"},
		{name: "opencode json", app: "opencode", files: map[string][]byte{"opencode.json": []byte(`{"model":"openrouter/deepseek/deepseek-chat-v3.1"}`)}, want: "openrouter/deepseek/deepseek-chat-v3.1"},
		{name: "opencode jsonc", app: "opencode", files: map[string][]byte{"opencode.jsonc": []byte("{\n  // selected during upload\n  \"model\": \"venice/llama-3.3-70b\"\n}\n")}, want: "venice/llama-3.3-70b"},
		{name: "missing", app: "codex", files: map[string][]byte{"config.toml": []byte("[projects.x]\nmodel = \"nested\"\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Model(tc.app, tc.files); got != tc.want {
				t.Fatalf("Model()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetModelOverridesEachAgentConfiguration(t *testing.T) {
	for _, tc := range []struct {
		app, file, input, want string
	}{
		{app: "claude", file: "settings.json", input: `{"permissions":{"defaultMode":"bypassPermissions"}}`, want: "claude-sonnet-4-5"},
		{app: "codex", file: "config.toml", input: "approval_policy = \"never\"\n[projects.x]\ntrust_level = \"trusted\"\n", want: "gpt-6-astra"},
		{app: "opencode", file: "opencode.json", input: `{"provider":{"openrouter":{}}}`, want: "openrouter/anthropic/claude-sonnet-4.5"},
		{app: "opencode", file: "opencode.jsonc", input: "{/* keep semantics */\n\"provider\": {\"venice\": {},},\n}\n", want: "venice/llama-3.3-70b"},
	} {
		t.Run(tc.app+tc.file, func(t *testing.T) {
			files := map[string][]byte{tc.file: []byte(tc.input)}
			if err := SetModel(tc.app, files, tc.want); err != nil {
				t.Fatal(err)
			}
			if got := Model(tc.app, files); got != tc.want {
				t.Fatalf("Model()=%q, want %q; config=%s", got, tc.want, files[tc.file])
			}
		})
	}
	if err := SetModel("claude", map[string][]byte{}, "bad\nmodel"); err == nil {
		t.Fatal("control character in model accepted")
	}
}
