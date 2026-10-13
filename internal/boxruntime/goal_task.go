package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func readGoalTaskFile(path string) (string, []byte, error) {
	root, err := os.OpenRoot(WorkspaceDirectory())
	if err != nil {
		return "", nil, fmt.Errorf("workspace unavailable")
	}
	defer root.Close()
	if filepath.IsAbs(path) {
		path, err = filepath.Rel(WorkspaceDirectory(), path)
		if err != nil {
			return "", nil, fmt.Errorf("file must be inside the workspace")
		}
	}
	file, err := root.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("workspace file unavailable")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 25<<20 {
		return "", nil, fmt.Errorf("result must be a regular file up to 25 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, (25<<20)+1))
	if err != nil || len(data) > 25<<20 {
		return "", nil, fmt.Errorf("result file exceeds 25 MiB")
	}
	return stat.Name(), data, nil
}

func callGoalTaskTool(ctx context.Context, assignment, name string, args json.RawMessage) (map[string]any, error) {
	var req struct {
		Text    string   `json:"text"`
		Message string   `json:"message"`
		Files   []string `json:"files"`
	}
	if json.Unmarshal(args, &req) != nil {
		return nil, fmt.Errorf("invalid task arguments")
	}
	var result any
	if name == "failed_task" {
		if len(strings.TrimSpace(req.Message)) == 0 || len(req.Message) > 20000 {
			return nil, fmt.Errorf("message must contain 1–20000 bytes")
		}
		if err := desktopAgentAPI(ctx, assignment, http.MethodPost, "/v1/agent-desktop/task/fail", map[string]string{"message": req.Message}, &result); err != nil {
			return nil, err
		}
	} else {
		if len(strings.TrimSpace(req.Text)) == 0 || len(req.Text) > 20000 || len(req.Files) > 8 {
			return nil, fmt.Errorf("text must contain 1–20000 bytes with at most eight files")
		}
		type file struct {
			Name      string `json:"name"`
			MediaType string `json:"mediaType"`
			Data      []byte `json:"data"`
		}
		files := []file{}
		for _, path := range req.Files {
			name, data, err := readGoalTaskFile(path)
			if err != nil {
				return nil, err
			}
			files = append(files, file{Name: name, MediaType: http.DetectContentType(data), Data: data})
		}
		if err := desktopAgentAPIWithTimeout(ctx, assignment, http.MethodPost, "/v1/agent-desktop/task/complete", "", map[string]any{"text": req.Text, "files": files}, &result, 2*time.Minute); err != nil {
			return nil, err
		}
	}
	data, _ := json.Marshal(result)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}}, nil
}

func goalTaskOptionsSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"goal"}, "additionalProperties": false, "properties": map[string]any{
		"goal":             map[string]any{"type": "string", "minLength": 1, "maxLength": 100000},
		"timeLimitSeconds": map[string]any{"type": "integer", "minimum": 300, "maximum": 604800},
		"retentionSeconds": map[string]any{"type": "integer", "minimum": 3600, "maximum": 604800},
		"links":            map[string]any{"type": "object", "required": []string{"kinds"}, "additionalProperties": false, "properties": map[string]any{"kinds": map[string]any{"type": "array", "minItems": 1, "maxItems": 3, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"control", "view", "screenshot"}}}, "ttlSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 604800}}},
	}}
}
