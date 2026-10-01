package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type desktopMultiCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func parseDesktopMultiCalls(raw json.RawMessage) ([]desktopMultiCall, error) {
	var request struct {
		Calls []desktopMultiCall `json:"calls"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || len(request.Calls) < 2 || len(request.Calls) > 8 {
		return nil, fmt.Errorf("multicall requires 2–8 calls")
	}
	known := make(map[string]bool)
	for _, tool := range desktopMCPTools() {
		known[tool["name"].(string)] = true
	}
	for _, call := range request.Calls {
		trimmed := bytes.TrimSpace(call.Arguments)
		if !known[call.Name] || call.Name == "multicall" || len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
			return nil, fmt.Errorf("multicall contains an unknown, nested, or invalid tool call")
		}
	}
	return request.Calls, nil
}

func desktopMultiCallNeedsSession(calls []desktopMultiCall) bool {
	for _, call := range calls {
		if desktopMCPChatTools[call.Name] {
			return true
		}
		if call.Name == "heartbeat" {
			var request struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(call.Arguments, &request)
			if request.Action == "start" {
				return true
			}
		}
	}
	return false
}

func executeDesktopTool(ctx context.Context, assignment, name string, arguments json.RawMessage, resolve desktopToolPolicyResolver) (map[string]any, error) {
	if name != "multicall" {
		return callDesktopTool(ctx, assignment, name, arguments)
	}
	calls, err := parseDesktopMultiCalls(arguments)
	if err != nil {
		return nil, err
	}
	_, allowed, err := allowedDesktopMCPTools(ctx, assignment, resolve)
	if err != nil {
		return nil, fmt.Errorf("MCP tool policy unavailable")
	}
	results := runDesktopMultiCalls(ctx, assignment, calls, allowed, callDesktopTool)
	data, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		return nil, fmt.Errorf("could not encode multicall results")
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}}, nil
}

func runDesktopMultiCalls(ctx context.Context, assignment string, calls []desktopMultiCall, allowed map[string]bool, invoke func(context.Context, string, string, json.RawMessage) (map[string]any, error)) []map[string]any {
	results := make([]map[string]any, len(calls))
	var group sync.WaitGroup
	for index, call := range calls {
		group.Add(1)
		go func() {
			defer group.Done()
			var result map[string]any
			var err error
			if !allowed[call.Name] {
				err = fmt.Errorf("MCP tool %s is not allowed for this box", call.Name)
			} else {
				callCtx, cancel := context.WithTimeout(ctx, desktopToolTimeout(call.Name))
				result, err = invoke(callCtx, assignment, call.Name, call.Arguments)
				cancel()
			}
			_ = queueLocalMCPActivity(assignment, call.Name, call.Arguments, err)
			if err != nil {
				results[index] = map[string]any{"name": call.Name, "isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}
			} else {
				results[index] = map[string]any{"name": call.Name, "isError": false, "result": result}
			}
		}()
	}
	group.Wait()
	return results
}
