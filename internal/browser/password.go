package browser

import (
	"context"
	"encoding/json"
	"fmt"
)

// PasswordTarget is a short-lived reference to one DOM element in one document.
// It must not be persisted or accepted from the agent as proof of authorization.
type PasswordTarget struct {
	Origin  string
	session string
	object  string
}

type runtimeResult struct {
	Result struct {
		ObjectID string          `json:"objectId"`
		Value    json.RawMessage `json:"value"`
	} `json:"result"`
	Exception json.RawMessage `json:"exceptionDetails"`
}

// FocusedPassword resolves only top-level password inputs. Iframes are rejected
// until they have their own explicit destination policy. Isolated-world code
// avoids trusting JavaScript functions overwritten by page scripts.
func (c *Client) FocusedPassword(ctx context.Context) (PasswordTarget, error) {
	var targets struct {
		Infos []struct {
			ID   string `json:"targetId"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return PasswordTarget{}, err
	}
	var found PasswordTarget
	for _, target := range targets.Infos {
		if target.Type != "page" {
			continue
		}
		origin, err := Origin(target.URL)
		if err != nil {
			continue
		}
		var attached struct {
			Session string `json:"sessionId"`
		}
		if err = c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached); err != nil {
			return PasswordTarget{}, err
		}
		session := attached.Session
		var tree struct {
			Tree struct {
				Frame struct {
					ID string `json:"id"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		if err = c.Call(ctx, session, "Page.getFrameTree", nil, &tree); err != nil {
			return PasswordTarget{}, err
		}
		var world struct {
			ID int64 `json:"executionContextId"`
		}
		if err = c.Call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": tree.Tree.Frame.ID, "worldName": "vmbox-private-input"}, &world); err != nil {
			return PasswordTarget{}, err
		}
		var result runtimeResult
		if err = c.Call(ctx, session, "Runtime.evaluate", map[string]any{"contextId": world.ID, "expression": passwordProbe, "returnByValue": false}, &result); err != nil {
			return PasswordTarget{}, err
		}
		if len(result.Exception) > 0 {
			return PasswordTarget{}, fmt.Errorf("browser destination unavailable")
		}
		if result.Result.ObjectID == "" {
			continue
		}
		if found.object != "" {
			return PasswordTarget{}, fmt.Errorf("browser focus is ambiguous")
		}
		found = PasswordTarget{Origin: origin, session: session, object: result.Result.ObjectID}
	}
	if found.object == "" {
		return PasswordTarget{}, fmt.Errorf("focus a top-level HTTPS password field in the managed browser")
	}
	return found, nil
}

const passwordProbe = `(() => {
 const e=document.activeElement;
 if (!document.hasFocus() || document.visibilityState!=="visible" || !(e instanceof HTMLInputElement) || e.type!=="password" || e.disabled || e.readOnly || !e.isConnected || !e.getClientRects().length) return null;
 return e;
})()`

// InsertPassword rechecks the exact element, focus and origin in the same JS
// execution that sets the value. Navigation invalidates the object reference.
// It returns status only and never submits a form or marks a secret confirmed.
func (c *Client) InsertPassword(ctx context.Context, target PasswordTarget, expectedOrigin string, value []byte) error {
	origin, err := Origin(expectedOrigin)
	if err != nil || origin != expectedOrigin || target.Origin != origin || target.object == "" || len(value) == 0 || len(value) > 4096 {
		return fmt.Errorf("password destination is not authorized")
	}
	var result runtimeResult
	err = c.Call(ctx, target.session, "Runtime.callFunctionOn", map[string]any{
		"objectId": target.object, "functionDeclaration": passwordInsert, "returnByValue": true,
		"arguments": []map[string]any{{"value": origin}, {"value": string(value)}},
	}, &result)
	if err != nil {
		return err
	}
	defer clear(result.Result.Value)
	defer clear(result.Exception)
	var ok bool
	if len(result.Exception) > 0 || json.Unmarshal(result.Result.Value, &ok) != nil || !ok {
		return fmt.Errorf("password destination changed or rejected entry")
	}
	return nil
}

const passwordInsert = `function(origin,value) {
 const d=this.ownerDocument;
 if (d!==document || d.location.origin!==origin || !d.hasFocus() || d.visibilityState!=="visible" || d.activeElement!==this || !(this instanceof HTMLInputElement) || this.type!=="password" || this.disabled || this.readOnly || !this.isConnected || !this.getClientRects().length) return false;
 if (this.maxLength>=0 && value.length>this.maxLength) return false;
 const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value").set;
 setter.call(this,value);
 this.dispatchEvent(new Event("input",{bubbles:true}));
 this.dispatchEvent(new Event("change",{bubbles:true}));
 return true;
}`
