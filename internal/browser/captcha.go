package browser

import (
	"context"
	"encoding/json"
	"net/url"
)

// CaptchaChallenge reports that an open page contains a challenge widget. It is
// deliberately coarse: it never reads site keys, tokens, or image bytes, and it
// never solves or submits anything.
type CaptchaChallenge struct {
	URL  string
	Type string
}

const captchaProbe = `(() => {
 const marks = [
  ["recaptcha", ['iframe[src*="google.com/recaptcha"]','iframe[src*="recaptcha/api2"]','iframe[src*="recaptcha/enterprise"]','.g-recaptcha']],
  ["hcaptcha", ['iframe[src*="hcaptcha.com"]','.h-captcha']],
  ["turnstile", ['iframe[src*="challenges.cloudflare.com"]','.cf-turnstile']],
  ["image", ['img[src*="captcha"]','input[name*="captcha"]']]
 ];
 for (const [type, selectors] of marks) {
  for (const selector of selectors) {
   try { if (document.querySelector(selector)) return type; } catch (e) { return ""; }
  }
 }
 return "";
})()`

// CaptchaChallenges inspects each open top-level page in an isolated world and
// classifies any challenge widget it finds. Cross-origin challenge iframes are
// matched by their element, so no frame-level access is needed.
func (c *Client) CaptchaChallenges(ctx context.Context) ([]CaptchaChallenge, error) {
	var targets struct {
		Infos []struct {
			ID   string `json:"targetId"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return nil, err
	}
	challenges := []CaptchaChallenge{}
	seen := map[string]bool{}
	for _, target := range targets.Infos {
		if target.Type != "page" {
			continue
		}
		parsed, err := url.Parse(target.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		var attached struct {
			Session string `json:"sessionId"`
		}
		if err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached); err != nil {
			return nil, err
		}
		session := attached.Session
		var tree struct {
			Tree struct {
				Frame struct {
					ID string `json:"id"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		var world struct {
			ID int64 `json:"executionContextId"`
		}
		var result runtimeResult
		if err := c.Call(ctx, session, "Page.getFrameTree", nil, &tree); err != nil {
			_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
			return nil, err
		}
		if err := c.Call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": tree.Tree.Frame.ID, "worldName": "vmbox-captcha-probe"}, &world); err != nil {
			_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
			return nil, err
		}
		err = c.Call(ctx, session, "Runtime.evaluate", map[string]any{"contextId": world.ID, "expression": captchaProbe, "returnByValue": true}, &result)
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
		if err != nil {
			return nil, err
		}
		if len(result.Exception) > 0 {
			continue
		}
		var challengeType string
		if json.Unmarshal(result.Result.Value, &challengeType) != nil || challengeType == "" {
			continue
		}
		key := challengeType + " " + target.URL
		if seen[key] {
			continue
		}
		seen[key] = true
		challenges = append(challenges, CaptchaChallenge{URL: target.URL, Type: challengeType})
	}
	return challenges, nil
}
