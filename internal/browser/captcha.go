package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// CaptchaChallenge reports that an open page contains a challenge widget. It
// is deliberately coarse: it never reads tokens, and it never solves or
// submits anything. SiteKey carries the widget's public site key when found.
type CaptchaChallenge struct {
	URL     string
	Type    string
	SiteKey string
}

// CaptchaRect is the challenge widget's on-page geometry in CSS pixels,
// relative to the document origin. Top-frame geometry comes from a probe that
// ran in the top document or a verified child frame — never from a
// page-supplied postMessage, which a page could forge to aim a capture.
type CaptchaRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// CaptchaCapture pairs a detected challenge with a debug PNG of the widget
// region as the page renders it. Site keys are public widget configuration;
// the PNG is pixels the owner could see on the screen anyway. Neither contains
// solver inputs or tokens.
type CaptchaCapture struct {
	Challenge CaptchaChallenge
	PNG       []byte
}

// CaptchaDetail is the extracted per-page challenge data: the widget type, its
// on-page geometry when it could be located, and the public site key that lets
// the chat UI re-render the same widget for the owner.
type CaptchaDetail struct {
	Type    string       `json:"type"`
	Rect    *CaptchaRect `json:"rect"`
	Sitekey string       `json:"sitekey"`
}

const maxCaptchaClipWidth = 1280
const maxCaptchaClipHeight = 1280

// captchaFrameNode is one node of Page.getFrameTree's frame tree.
type captchaFrameNode struct {
	Frame struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"frame"`
	ChildFrames []captchaFrameNode `json:"childFrames"`
}

const captchaProbe = `(() => {
 const marks = [
  ["recaptcha", ['iframe[src*="google.com/recaptcha"]','iframe[src*="recaptcha/api2"]','iframe[src*="recaptcha/enterprise"]','.g-recaptcha']],
  ["hcaptcha", ['iframe[src*="hcaptcha.com"]','.h-captcha']],
  ["turnstile", ['iframe[src*="challenges.cloudflare.com"]','.cf-turnstile']],
  ["image", ['form img[src*="captcha"]','input[name*="captcha"]']]
 ];
 for (const [type, selectors] of marks) {
  for (const selector of selectors) {
   try { if (document.querySelector(selector)) return type; } catch (e) { return ""; }
  }
 }
 // No widget in this document: a captcha inside an embedded login iframe is
 // reported by the vmbox-captcha-guard add-on in the child frame, which marks
 // the top document through its postMessage channel.
 try {
  const marker = JSON.parse(document.documentElement.getAttribute("data-vmbox-captcha") || "null");
  if (marker && marker.type) return marker.type;
 } catch (e) {}
 return "";
})()`

// captchaDetailProbe extracts the challenge type, geometry and public site key
// for a debug capture and answer routing. It first locates the widget element
// directly, then falls back to the vmbox-captcha-guard add-on's extracted
// marker data, which carries the same details for widgets the add-on detected.
// Page coordinates include scroll offsets so a clip works with
// captureBeyondViewport. The IIFE returns a plain object; returnByValue keeps
// it as one JSON object.
const captchaDetailProbe = `(() => {
 const marks = [
  ["recaptcha", ['iframe[src*="google.com/recaptcha"]','iframe[src*="recaptcha/api2"]','iframe[src*="recaptcha/enterprise"]','.g-recaptcha']],
  ["hcaptcha", ['iframe[src*="hcaptcha.com"]','.h-captcha']],
  ["turnstile", ['iframe[src*="challenges.cloudflare.com"]','.cf-turnstile']],
  ["image", ['form img[src*="captcha"]','input[name*="captcha"]']]
 ];
 let type = "", rect = null, sitekey = "";
 for (const [t, selectors] of marks) {
  for (const selector of selectors) {
   try {
    const el = document.querySelector(selector);
    if (el) {
     type = t;
     const box = el.getBoundingClientRect();
     rect = {x: box.x + window.scrollX, y: box.y + window.scrollY, width: box.width, height: box.height};
     break;
    }
   } catch (e) {}
  }
  if (type) break;
 }
 if (type) {
  const containers = {recaptcha: ".g-recaptcha", hcaptcha: ".h-captcha", turnstile: ".cf-turnstile"};
  const frames = {
   recaptcha: 'iframe[src*="google.com/recaptcha"],iframe[src*="recaptcha/api2"],iframe[src*="recaptcha/enterprise"]',
   hcaptcha: 'iframe[src*="hcaptcha.com"]',
   turnstile: 'iframe[src*="challenges.cloudflare.com"]'
  };
  try {
   const container = document.querySelector(containers[type] || "");
   if (container && container.dataset && container.dataset.sitekey) sitekey = container.dataset.sitekey;
   if (!sitekey) {
    const frame = document.querySelector(frames[type] || "");
    const src = frame && frame.src;
    if (src) {
     const parsed = new URL(src);
     sitekey = parsed.searchParams.get("sitekey") || parsed.searchParams.get("k") || parsed.searchParams.get("key") || "";
    }
   }
  } catch (e) {}
 }
 if (!type) {
  // Only the add-on's own marker for this document remains. A marker written
  // from a CHILD frame's postMessage (dataUntreated=1) is untrusted: a page
  // can forge it to aim a capture anywhere. Report the type for detection but
  // no page-supplied geometry; the Go side verifies the widget inside the
  // child frame over CDP and measures there.
  try {
   const marker = JSON.parse(document.documentElement.getAttribute("data-vmbox-captcha") || "null");
   if (marker && marker.type) {
    type = marker.type;
    if (!marker.dataUntreated && marker.rect && marker.rect.width > 0 && marker.rect.height > 0) {
     rect = {x: marker.rect.x + window.scrollX, y: marker.rect.y + window.scrollY, width: marker.rect.width, height: marker.rect.height};
    }
    if (!marker.dataUntreated && marker.sitekey) sitekey = String(marker.sitekey);
   }
  } catch (e) {}
 }
 return {type, rect, sitekey};
})()`

// captchaFrameProbe verifies and measures a widget inside ONE child frame. The
// isolated world shares the frame's DOM, so a positive match is this frame's
// own content — not a forged postMessage. Rect is frame-local in CSS pixels.
const captchaFrameProbe = `(() => {
 const marks = [
  ["recaptcha", ['.g-recaptcha','iframe[src*="google.com/recaptcha"]','iframe[src*="recaptcha/api2"]','iframe[src*="recaptcha/enterprise"]']],
  ["hcaptcha", ['.h-captcha','iframe[src*="hcaptcha.com"]']],
  ["turnstile", ['.cf-turnstile','iframe[src*="challenges.cloudflare.com"]']],
  ["image", ['form img[src*="captcha"]','input[name*="captcha"]']]
 ];
 let type = "", rect = null, sitekey = "";
 for (const [t, selectors] of marks) {
  for (const selector of selectors) {
   try {
    const el = document.querySelector(selector);
    if (el) {
     type = t;
     const box = el.getBoundingClientRect();
     rect = {x: box.x, y: box.y, width: box.width, height: box.height};
     try {
      const container = document.querySelector(".g-recaptcha,.h-captcha,.cf-turnstile");
      if (container && container.dataset && container.dataset.sitekey) sitekey = container.dataset.sitekey;
     } catch (e) {}
     break;
    }
   } catch (e) {}
  }
  if (type) break;
 }
 return {type, rect, sitekey};
})()`

// verifyCaptchaDetail resolves one page's challenge through trusted steps only:
// the top document's own probe result, or — when only an untrusted child-frame
// marker is present — the frame probe re-run inside the child frame over CDP,
// with the top-page rectangle composed from the frame chain geometry.
func (c *Client) verifyCaptchaDetail(ctx context.Context, session string, root captchaFrameNode) (*CaptchaDetail, string, error) {
	detail, err := c.evaluateCaptchaDetailProbe(ctx, session, root.Frame.ID, captchaDetailProbe)
	if err != nil {
		return nil, "", err
	}
	if detail.Type == "" {
		return nil, "", nil
	}
	if detail.Rect != nil {
		// Direct match in this document: trusted geometry from a probe that
		// shared this DOM.
		return &detail, root.Frame.ID, nil
	}
	// A marker reported from a child frame. Find and verify it there.
	var verified *CaptchaDetail
	verifiedID := ""
	var walk func(node captchaFrameNode) bool
	walk = func(node captchaFrameNode) bool {
		for _, child := range node.ChildFrames {
			childDetail, err := c.evaluateCaptchaDetailProbe(ctx, session, child.Frame.ID, captchaFrameProbe)
			if err != nil {
				continue
			}
			if childDetail.Type == "" {
				continue
			}
			// Confirm the marker's type against the frame's own content.
			if detail.Type != "" && childDetail.Type != detail.Type {
				continue
			}
			childDetail.Rect = c.composeFrameRect(ctx, session, childDetail.Rect, child.Frame.ID, root)
			verified = &childDetail
			verifiedID = child.Frame.ID
			return true
		}
		for _, child := range node.ChildFrames {
			if walk(child) {
				return true
			}
		}
		return false
	}
	if !walk(root) {
		// The marker claims a widget but no child frame verifies it: treat the
		// detection as forged noise.
		return &CaptchaDetail{}, "", nil
	}
	return verified, verifiedID, nil
}

// composeFrameRect translates a frame-local widget rectangle into top-page
// coordinates. The frame tree lists ids, not boxes, so each ancestor iframe's
// owning element is resolved via DOM.getFrameOwner and measured inside its
// parent frame; the offsets accumulate down the chain.
func (c *Client) composeFrameRect(ctx context.Context, session string, rect *CaptchaRect, frameID string, root captchaFrameNode) *CaptchaRect {
	if rect == nil {
		return nil
	}
	chain := frameChain(root, frameID)
	if len(chain) < 2 {
		return nil
	}
	// Each ancestor iframe's viewport position is measured inside its parent
	// frame (chain[0] is the top document). Composed top-to-bottom, the
	// offsets accumulate into top-page coordinates.
	offsetX, offsetY := 0.0, 0.0
	for i := 0; i+1 < len(chain); i++ {
		box, ok := c.evaluateCaptchaFrameBox(ctx, session, chain[i], chain[i+1])
		if !ok {
			return nil
		}
		offsetX += box.X
		offsetY += box.Y
	}
	return &CaptchaRect{X: rect.X + offsetX, Y: rect.Y + offsetY, Width: rect.Width, Height: rect.Height}
}

// evaluateCaptchaFrameBox measures one child frame's viewport position inside
// its parent frame by locating the iframe element the browser's frame tree
// position implies. It matches by frame URL because the element carries no id.
func (c *Client) evaluateCaptchaFrameBox(ctx context.Context, session, parentFrameID, childFrameID string) (CaptchaRect, bool) {
	// The frame tree does not map ids to elements; use DOM.getFrameOwner to
	// resolve the owning element, then measure it in the parent's world.
	var owner struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	if err := c.Call(ctx, session, "DOM.getFrameOwner", map[string]any{"frameId": childFrameID}, &owner); err != nil || owner.BackendNodeID == 0 {
		return CaptchaRect{}, false
	}
	var world struct {
		ID int64 `json:"executionContextId"`
	}
	if err := c.Call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": parentFrameID, "worldName": "vmbox-captcha-probe"}, &world); err != nil {
		return CaptchaRect{}, false
	}
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := c.Call(ctx, session, "DOM.resolveNode", map[string]any{"backendNodeId": owner.BackendNodeID, "executionContextId": world.ID}, &resolved); err != nil || resolved.Object.ObjectID == "" {
		return CaptchaRect{}, false
	}
	var box runtimeResult
	if err := c.Call(ctx, session, "Runtime.callFunctionOn", map[string]any{
		"objectId":            resolved.Object.ObjectID,
		"functionDeclaration": "function(){const r=this.getBoundingClientRect();return {x:r.x+window.scrollX,y:r.y+window.scrollY,width:r.width,height:r.height};}",
		"returnByValue":       true,
	}, &box); err != nil {
		return CaptchaRect{}, false
	}
	var result struct {
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	}
	if json.Unmarshal(box.Result.Value, &result) != nil || result.Width <= 0 || result.Height <= 0 {
		return CaptchaRect{}, false
	}
	return CaptchaRect(result), true
}

// frameChain lists the frame ids from the root down to the widget frame.
func frameChain(root captchaFrameNode, frameID string) []string {
	var walk func(node captchaFrameNode, path []string) []string
	walk = func(node captchaFrameNode, path []string) []string {
		path = append(path, node.Frame.ID)
		if node.Frame.ID == frameID {
			return path
		}
		for _, child := range node.ChildFrames {
			if found := walk(child, path); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(root, nil)
}

// evaluateCaptchaDetailProbe runs a probe expression in one frame's isolated
// world and decodes its object result. Exception and decode failures yield an
// empty detail instead of aborting the scan.
func (c *Client) evaluateCaptchaDetailProbe(ctx context.Context, session, frameID, expression string) (CaptchaDetail, error) {
	detail := CaptchaDetail{}
	var world struct {
		ID int64 `json:"executionContextId"`
	}
	if err := c.Call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": frameID, "worldName": "vmbox-captcha-probe"}, &world); err != nil {
		return detail, nil
	}
	var result runtimeResult
	if err := c.Call(ctx, session, "Runtime.evaluate", map[string]any{"contextId": world.ID, "expression": expression, "returnByValue": true}, &result); err != nil {
		return detail, nil
	}
	if len(result.Exception) > 0 {
		return detail, nil
	}
	_ = json.Unmarshal(result.Result.Value, &detail)
	return detail, nil
}

// clipCaptchaRect validates an extracted widget geometry and caps it so one
// oversized or off-screen widget cannot produce a huge capture.
func clipCaptchaRect(rect *CaptchaRect) (float64, float64, float64, float64, bool) {
	if rect == nil {
		return 0, 0, 0, 0, false
	}
	x, y := rect.X, rect.Y
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	width, height := rect.Width, rect.Height
	if width > maxCaptchaClipWidth {
		width = maxCaptchaClipWidth
	}
	if height > maxCaptchaClipHeight {
		height = maxCaptchaClipHeight
	}
	if width < 1 || height < 1 {
		return 0, 0, 0, 0, false
	}
	return x, y, width, height, true
}

// CaptchaCaptures inspects each open top-level page like CaptchaChallenges and,
// when a widget is found, captures its on-page region as a PNG. Detection is
// still read-only and coarse; the capture is debug pixels for the owner's chat.
func (c *Client) CaptchaCaptures(ctx context.Context) ([]CaptchaCapture, error) {
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
	captures := []CaptchaCapture{}
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
			// A just-navigated or defunct target can refuse attachment; skip it
			// instead of failing the whole scan.
			continue
		}
		session := attached.Session
		capture, found, err := c.captchaCaptureForTarget(ctx, session, target.URL)
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		key := capture.Challenge.Type + " " + target.URL
		if seen[key] {
			continue
		}
		seen[key] = true
		captures = append(captures, capture)
	}
	return captures, nil
}

func (c *Client) captchaCaptureForTarget(ctx context.Context, session, targetURL string) (CaptchaCapture, bool, error) {
	var tree struct {
		Tree captchaFrameNode `json:"frameTree"`
	}
	if err := c.Call(ctx, session, "Page.getFrameTree", nil, &tree); err != nil {
		return CaptchaCapture{}, false, err
	}
	// The top document reports its own widget, or an UNTREATED marker written
	// by a child frame's postMessage. Forged markers (dataUntreated=1) carry no
	// trusted geometry: this code re-verifies the widget inside the child frame
	// the marker claims, measures there, and composes the top-page rectangle
	// from the frame chain — never from page-supplied coordinates.
	detail, _, err := c.verifyCaptchaDetail(ctx, session, tree.Tree)
	if err != nil {
		return CaptchaCapture{}, false, err
	}
	if detail == nil || detail.Type == "" {
		return CaptchaCapture{}, false, nil
	}
	capture := CaptchaCapture{Challenge: CaptchaChallenge{URL: targetURL, Type: detail.Type, SiteKey: detail.Sitekey}}
	if x, y, width, height, ok := clipCaptchaRect(detail.Rect); ok {
		var screenshot struct {
			Data string `json:"data"`
		}
		err := c.Call(ctx, session, "Page.captureScreenshot", map[string]any{
			"format":                "png",
			"captureBeyondViewport": true,
			"clip":                  map[string]any{"x": x, "y": y, "width": width, "height": height, "scale": 1},
		}, &screenshot)
		if err != nil {
			return CaptchaCapture{}, false, err
		}
		png, err := base64.StdEncoding.DecodeString(screenshot.Data)
		if err != nil {
			return CaptchaCapture{}, false, fmt.Errorf("invalid captcha capture")
		}
		capture.PNG = png
	}
	return capture, true, nil
}

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

// CaptchaAnswer carries the owner's solution from the chat UI to the managed
// browser. Widget challenges (reCAPTCHA, hCaptcha, Turnstile) bring a response
// token the chat's embedded widget produced; image challenges bring the typed
// answer. PageURL pins the injection to the captured tab. Token-like secrets
// exist only inside this box and are never returned.
type CaptchaAnswer struct {
	Type    string `json:"type"`
	Token   string `json:"token,omitempty"`
	Text    string `json:"text,omitempty"`
	PageURL string `json:"pageUrl,omitempty"`
}

// Validate enforces the same rules as DecodeCaptchaAnswer for direct callers.
func (a *CaptchaAnswer) Validate() error {
	switch a.Type {
	case "recaptcha", "hcaptcha", "turnstile":
		if len(a.Token) < 10 || len(a.Token) > 8192 {
			return fmt.Errorf("invalid widget response token")
		}
	case "image":
		if text := strings.TrimSpace(a.Text); text == "" || len(text) > 200 {
			return fmt.Errorf("provide the challenge answer text (1–200 characters)")
		}
	default:
		return fmt.Errorf("unsupported captcha type")
	}
	return nil
}

// SubmitCaptchaAnswer injects the owner's solution into the page that still
// shows a challenge. It runs in the page's main world because provider widgets
// dispatch their callback from there, then gives verification a short window.
func (c *Client) SubmitCaptchaAnswer(ctx context.Context, answer CaptchaAnswer) error {
	if err := answer.Validate(); err != nil {
		return err
	}
	var targets struct {
		Infos []struct {
			ID   string `json:"targetId"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return err
	}
	widgets := []string{}
	applied := false
	for _, target := range targets.Infos {
		if target.Type == "iframe" && captchaProviderFrame(answer.Type, target.URL) == "widget" {
			widgets = append(widgets, target.ID)
			continue
		}
		if target.Type != "page" {
			continue
		}
		parsed, err := url.Parse(target.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		// Only inject into the page the card was captured from, so a token can
		// never land on an unrelated tab that happens to show the same type.
		if answer.PageURL != "" && !sameCaptchaPage(answer.PageURL, target.URL) {
			continue
		}
		if applied {
			continue
		}
		var attached struct {
			Session string `json:"sessionId"`
		}
		if err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached); err != nil {
			continue
		}
		session := attached.Session
		found, err := c.applyCaptchaAnswer(ctx, session, answer)
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
		if err != nil {
			return err
		}
		if found {
			applied = true
		}
	}
	// The token also has to land inside the provider's own frame: reCAPTCHA and
	// hCaptcha store the response there. Out-of-process widget frames appear as
	// their own targets under site isolation; best effort when they do not.
	for _, widgetID := range widgets {
		var attached struct {
			Session string `json:"sessionId"`
		}
		if err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": widgetID, "flatten": true}, &attached); err != nil {
			continue
		}
		_, _ = c.evaluateCaptchaSnippet(ctx, attached.Session, captchaWidgetSnippet(answer.Type, jsonQuote(answer.Token)))
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": attached.Session}, nil)
	}
	if !applied {
		return fmt.Errorf("no matching captcha challenge is currently open in the managed browser")
	}
	return nil
}

// sameCaptchaPage compares the card's captured URL with an open target. Scheme
// and host must match, and the path must not have moved to another document.
// An unpinned answer (no card URL) may target any open page.
func sameCaptchaPage(cardURL, targetURL string) bool {
	card, err := url.Parse(cardURL)
	if err != nil || card.Host == "" {
		return true
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(card.Scheme, target.Scheme) && strings.EqualFold(card.Host, target.Host) && card.Path == target.Path
}

// captchaProviderFrame classifies one frame URL for the answer routing. Only
// the provider's real origins match: a substring anywhere in the URL would let
// an attacker page like https://evil.test/?x=google.com/recaptcha pose as a
// widget frame and read a valid token.
func captchaProviderFrame(kind, frameURL string) string {
	parsed, err := url.Parse(frameURL)
	if err != nil {
		return "page"
	}
	host := strings.ToLower(parsed.Host)
	path := strings.ToLower(parsed.Path)
	switch kind {
	case "recaptcha":
		if host == "www.google.com" && (strings.HasPrefix(path, "/recaptcha/") || strings.HasPrefix(path, "/recaptcha")) {
			return "widget"
		}
		if host == "recaptcha.google.com" {
			return "widget"
		}
		if host == "www.gstatic.com" && strings.HasPrefix(path, "/recaptcha/") {
			return "widget"
		}
	case "hcaptcha":
		if host == "js.hcaptcha.com" || host == "newassets.hcaptcha.com" || host == "api.hcaptcha.com" || host == "accounts.hcaptcha.com" {
			return "widget"
		}
	case "turnstile":
		if host == "challenges.cloudflare.com" {
			return "widget"
		}
	}
	return "page"
}

// jsonQuote renders a Go string as a JSON string literal. JSON string syntax is
// valid JavaScript string syntax, so the literal can be embedded in snippets.
func jsonQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}

// applyCaptchaAnswer injects the answer into the page's main world, which is
// the only place a provider callback can be invoked.
func (c *Client) applyCaptchaAnswer(ctx context.Context, session string, answer CaptchaAnswer) (bool, error) {
	result, err := c.evaluateCaptchaSnippet(ctx, session, captchaPageSnippet(answer, jsonQuote(answer.Token), jsonQuote(answer.Text)))
	if err != nil {
		return false, err
	}
	return result != "" && result != "no-target", nil
}

func (c *Client) evaluateCaptchaSnippet(ctx context.Context, session, expression string) (string, error) {
	var result runtimeResult
	if err := c.Call(ctx, session, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &result); err != nil {
		return "", err
	}
	if len(result.Exception) > 0 {
		return "", nil
	}
	var value string
	if json.Unmarshal(result.Result.Value, &value) != nil {
		return "", nil
	}
	return value, nil
}

// captchaWidgetSnippet stores the token inside the provider's own frame so the
// widget's internal state agrees with the answer being submitted.
func captchaWidgetSnippet(kind, tokenLiteral string) string {
	selector := `textarea#g-recaptcha-response,textarea[name="g-recaptcha-response"]`
	if kind == "hcaptcha" {
		selector = `textarea[name="h-captcha-response"],textarea[name="g-recaptcha-response"]`
	}
	return `(() => {
  const field = document.querySelector(` + jsonQuote(selector) + `) || document.querySelector("textarea");
  if (!field) return "";
  field.value = ` + tokenLiteral + `;
  field.dispatchEvent(new Event("input", {bubbles: true}));
  field.dispatchEvent(new Event("change", {bubbles: true}));
  return "ok";
})()`
}

// captchaPageSnippet runs in the page's own main world: it fills the public
// response field, invokes the site's configured callback when discoverable, and
// otherwise submits the form containing the widget.
func captchaPageSnippet(answer CaptchaAnswer, tokenLiteral, textLiteral string) string {
	switch answer.Type {
	case "recaptcha":
		return `(() => {
  const token = ` + tokenLiteral + `;
  let filled = false;
  for (const selector of ["textarea#g-recaptcha-response", "textarea[id^='g-recaptcha-response']", "textarea[name='g-recaptcha-response']"]) {
    const field = document.querySelector(selector);
    if (field) { field.value = token; field.dispatchEvent(new Event("input", {bubbles: true})); filled = true; }
  }
  let called = false;
  const find = (object, depth) => {
    if (called || !object || typeof object !== "object" || depth > 8) return;
    for (const key of Object.keys(object)) {
      if (called) return;
      const value = object[key];
      if (typeof value === "function") {
        if (key === "callback") { try { value(token); called = true; } catch (e) {} }
        continue;
      }
      find(value, depth + 1);
    }
  };
  const clients = window.___grecaptcha_cfg && window.___grecaptcha_cfg.clients;
  if (clients) { for (const key of Object.keys(clients)) { find(clients[key], 0); if (called) break; } }
  if (called) return "callback";
  if (filled) {
    const host = document.querySelector(".g-recaptcha");
    const form = host && host.closest("form");
    if (form && typeof form.requestSubmit === "function") { form.requestSubmit(); return "submitted"; }
    return "textarea";
  }
  return "no-target";
})()`
	case "hcaptcha":
		return `(() => {
  const token = ` + tokenLiteral + `;
  let filled = false;
  for (const selector of ["textarea[name='h-captcha-response']", "textarea[name='g-recaptcha-response']", "[id^='h-captcha-response']"]) {
    const field = document.querySelector(selector);
    if (field) { field.value = token; field.dispatchEvent(new Event("input", {bubbles: true})); filled = true; }
  }
  if (filled) {
    const host = document.querySelector(".h-captcha");
    const form = host && host.closest("form");
    if (form && typeof form.requestSubmit === "function") { form.requestSubmit(); return "submitted"; }
    return "textarea";
  }
  return "no-target";
})()`
	case "turnstile":
		return `(() => {
  const token = ` + tokenLiteral + `;
  let filled = false;
  for (const selector of ["input[name='cf-turnstile-response']", "[id^='cf-turnstile-response']"]) {
    const field = document.querySelector(selector);
    if (field) { field.value = token; field.dispatchEvent(new Event("input", {bubbles: true})); filled = true; }
  }
  if (filled) {
    const host = document.querySelector(".cf-turnstile");
    const form = (host && host.closest("form")) || (filled && document.querySelector("form"));
    if (form && typeof form.requestSubmit === "function") { form.requestSubmit(); return "submitted"; }
    return "filled";
  }
  return "no-target";
})()`
	default:
		return `(() => {
  const text = ` + textLiteral + `;
  const field = document.querySelector("input[name*='captcha' i]");
  if (!field) return "no-target";
  // React-controlled inputs reset plain .value writes; go through the native
  // setter so the framework's state updates with us.
  const inputSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
  inputSetter.call(field, text);
  field.dispatchEvent(new Event("input", {bubbles: true}));
  field.dispatchEvent(new Event("change", {bubbles: true}));
  const form = field.closest("form");
  if (form && typeof form.requestSubmit === "function") { form.requestSubmit(); return "submitted"; }
  return "filled";
})()`
	}
}
