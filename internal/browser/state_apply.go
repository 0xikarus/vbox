package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ApplyState updates the live browser through CDP, never through profile files.
// A multi-origin import is not atomic: failure may leave earlier origins applied.
// The caller retains the encrypted import for an explicit retry.
func (c *Client) ApplyState(ctx context.Context, state StateImport) error {
	if err := state.Validate(); err != nil {
		return err
	}
	for _, site := range state.Origins {
		if len(site.LocalStorage) > 0 {
			if err := c.applyLocalStorage(ctx, site); err != nil {
				return fmt.Errorf("browser storage import incomplete; earlier entries may have been applied")
			}
		}
		cookies := make([]map[string]any, 0, len(site.Cookies))
		for _, cookie := range site.Cookies {
			value := map[string]any{"name": cookie.Name, "value": cookie.Value, "url": site.Origin + cookie.Path, "path": cookie.Path, "secure": true, "httpOnly": cookie.HTTPOnly}
			if cookie.SameSite != "" {
				value["sameSite"] = cookie.SameSite
			}
			if cookie.Expires != nil {
				value["expires"] = *cookie.Expires
			}
			cookies = append(cookies, value)
		}
		if len(cookies) > 0 {
			if err := c.Call(ctx, "", "Storage.setCookies", map[string]any{"cookies": cookies}, nil); err != nil {
				return fmt.Errorf("cookie import incomplete; earlier entries may have been applied")
			}
		}
	}
	return nil
}

func (c *Client) applyLocalStorage(ctx context.Context, site OriginState) error {
	var target struct {
		ID string `json:"targetId"`
	}
	if err := c.Call(ctx, "", "Target.createTarget", map[string]any{"url": site.Origin + "/", "background": true}, &target); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Call(cleanup, "", "Target.closeTarget", map[string]any{"targetId": target.ID}, nil)
	}()
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached); err != nil {
		return err
	}
	var frameID string
	for attempt := 0; attempt < 50; attempt++ {
		var tree struct {
			Tree struct {
				Frame struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		if err := c.Call(ctx, attached.Session, "Page.getFrameTree", nil, &tree); err != nil {
			return err
		}
		actual, err := Origin(tree.Tree.Frame.URL)
		if err == nil && actual == site.Origin {
			frameID = tree.Tree.Frame.ID
			break
		}
		if tree.Tree.Frame.URL != "" && tree.Tree.Frame.URL != "about:blank" {
			return fmt.Errorf("storage destination redirected")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if frameID == "" {
		return fmt.Errorf("storage destination unavailable")
	}
	var world struct {
		ID int64 `json:"executionContextId"`
	}
	if err := c.Call(ctx, attached.Session, "Page.createIsolatedWorld", map[string]any{"frameId": frameID, "worldName": "vmbox-private-storage"}, &world); err != nil {
		return err
	}
	var result runtimeResult
	if err := c.Call(ctx, attached.Session, "Runtime.callFunctionOn", map[string]any{"executionContextId": world.ID, "functionDeclaration": localStorageImport, "arguments": []map[string]any{{"value": site.Origin}, {"value": site.LocalStorage}}, "returnByValue": true}, &result); err != nil {
		return err
	}
	defer clear(result.Result.Value)
	defer clear(result.Exception)
	var ok bool
	if len(result.Exception) > 0 || json.Unmarshal(result.Result.Value, &ok) != nil || !ok {
		return fmt.Errorf("storage destination rejected import")
	}
	return nil
}

const localStorageImport = `function(origin,entries) {
 if (location.origin!==origin) return false;
 for (const entry of entries) localStorage.setItem(entry.name,entry.value);
 return true;
}`
