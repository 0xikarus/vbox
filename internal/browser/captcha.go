package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// CaptchaChallenge reports that an open page contains a challenge widget. It
// is deliberately coarse: it never reads tokens, and it never solves or
// submits anything. SiteKey carries the widget's public site key when found.
type CaptchaChallenge struct {
	URL      string
	Type     string
	SiteKey  string
	TargetID string
}

// CaptchaRect is the challenge widget's on-page geometry in CSS pixels,
// relative to the document origin.
type CaptchaRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// CaptchaCapture pairs a challenge with a PNG for the owner's chat. Image
// challenges use decoded image pixels when readable; SolverPNG contains only
// those image pixels and is the sole input to the automatic image solver.
type CaptchaCapture struct {
	Challenge CaptchaChallenge
	PNG       []byte
	SolverPNG []byte
}

// CaptchaDetail is the extracted per-page challenge data: the widget type, its
// on-page geometry when it could be located, and the public site key that lets
// the chat UI re-render the same widget for the owner.
type CaptchaDetail struct {
	Type      string       `json:"type"`
	Rect      *CaptchaRect `json:"rect"`
	Sitekey   string       `json:"sitekey"`
	SolverPNG []byte       `json:"-"`
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

type captchaTargetInfo struct {
	ID            string `json:"targetId"`
	Type          string `json:"type"`
	URL           string `json:"url"`
	ParentFrameID string `json:"parentFrameId"`
}

// captchaDetailProbe extracts the challenge type, geometry and public site key
// for a debug capture and answer routing. It locates the widget in this frame's
// DOM; the add-on's shared DOM marker is writable by the page and is not trusted.
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
  if (t === "image") {
   const image = document.querySelector('form img[src*="captcha"]');
   if (image && (!image.complete || !image.naturalWidth || !image.naturalHeight)) break;
  }
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
 return {type, rect, sitekey};
})()`

// captchaFrameProbe verifies and measures a widget inside one child frame.
// Its rectangle is local to that frame's viewport.
const captchaFrameProbe = `(() => {
 const marks = [
  ["recaptcha", ['.g-recaptcha','iframe[src*="google.com/recaptcha"]','iframe[src*="recaptcha/api2"]','iframe[src*="recaptcha/enterprise"]']],
  ["hcaptcha", ['.h-captcha','iframe[src*="hcaptcha.com"]']],
  ["turnstile", ['.cf-turnstile','iframe[src*="challenges.cloudflare.com"]']],
  ["image", ['form img[src*="captcha"]','input[name*="captcha"]']]
 ];
 let type = "", rect = null, sitekey = "";
 for (const [t, selectors] of marks) {
  if (t === "image") {
   const image = document.querySelector('form img[src*="captcha"]');
   if (image && (!image.complete || !image.naturalWidth || !image.naturalHeight)) break;
  }
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
      if (!sitekey && t !== "image") {
       const frame = document.querySelector({recaptcha:'iframe[src*="google.com/recaptcha"],iframe[src*="recaptcha/api2"],iframe[src*="recaptcha/enterprise"]',hcaptcha:'iframe[src*="hcaptcha.com"]',turnstile:'iframe[src*="challenges.cloudflare.com"]'}[t]);
       if (frame && frame.src) {
        const parsed = new URL(frame.src);
        sitekey = parsed.searchParams.get("sitekey") || parsed.searchParams.get("k") || parsed.searchParams.get("key") || "";
       }
      }
     } catch (e) {}
     break;
    }
   } catch (e) {}
  }
  if (type) break;
 }
 return {type, rect, sitekey};
})()`

// Extract actual image pixels for the third-party image solver. A screenshot
// can include pixels from other frames underneath a transparent page element;
// a canvas becomes unreadable for cross-origin images without CORS, so those
// challenges stay manual instead of uploading unrelated page content.
const captchaImageSourceProbe = `(() => {
 const image = document.querySelector('form img[src*="captcha"]');
 if (!image || !image.complete || !image.naturalWidth || !image.naturalHeight) return "";
 const scale = Math.min(1, 1280 / image.naturalWidth, 1280 / image.naturalHeight);
 const canvas = document.createElement("canvas");
 canvas.width = Math.max(1, Math.round(image.naturalWidth * scale));
 canvas.height = Math.max(1, Math.round(image.naturalHeight * scale));
 try {
  canvas.getContext("2d").drawImage(image, 0, 0, canvas.width, canvas.height);
  return canvas.toDataURL("image/png").split(",")[1] || "";
 } catch (e) { return ""; }
})()`

func (c *Client) captchaSolverImage(ctx context.Context, session, frameID string) []byte {
	var world struct {
		ID int64 `json:"executionContextId"`
	}
	if c.Call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": frameID, "worldName": "vmbox-captcha-probe"}, &world) != nil {
		return nil
	}
	var result runtimeResult
	if c.Call(ctx, session, "Runtime.evaluate", map[string]any{"contextId": world.ID, "expression": captchaImageSourceProbe, "returnByValue": true}, &result) != nil || len(result.Exception) > 0 {
		return nil
	}
	var encoded string
	if json.Unmarshal(result.Result.Value, &encoded) != nil || len(encoded) > 4<<20 {
		return nil
	}
	png, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(png) == 0 || len(png) > 3<<20 {
		return nil
	}
	return png
}

// verifyCaptchaDetail inspects one CDP target's root and same-process child
// frames. Out-of-process iframe targets are scanned separately by the caller.
func (c *Client) verifyCaptchaDetail(ctx context.Context, session string, root captchaFrameNode) (*CaptchaDetail, string, error) {
	detail, err := c.evaluateCaptchaDetailProbe(ctx, session, root.Frame.ID, captchaDetailProbe)
	if err != nil {
		return nil, "", err
	}
	if detail.Type != "" {
		return &detail, root.Frame.ID, nil
	}
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
		return nil, "", nil
	}
	return verified, verifiedID, nil
}

// composeFrameRect translates a frame-local widget rectangle into target-root
// document coordinates. At every boundary it clips to the containing iframe,
// so a child cannot aim a screenshot outside its visible frame.
func (c *Client) composeFrameRect(ctx context.Context, session string, rect *CaptchaRect, frameID string, root captchaFrameNode) *CaptchaRect {
	if rect == nil {
		return nil
	}
	chain := frameChain(root, frameID)
	if len(chain) < 2 {
		return nil
	}
	composed := *rect
	for i := len(chain) - 2; i >= 0; i-- {
		box, ok := c.evaluateCaptchaFrameBox(ctx, session, chain[i], chain[i+1], i == 0)
		if !ok {
			return nil
		}
		visible, ok := intersectCaptchaRect(composed, CaptchaRect{Width: box.Width, Height: box.Height})
		if !ok {
			return nil
		}
		composed = visible
		composed.X += box.X
		composed.Y += box.Y
	}
	return &composed
}

func intersectCaptchaRect(a, b CaptchaRect) (CaptchaRect, bool) {
	x, y := max(a.X, b.X), max(a.Y, b.Y)
	right, bottom := min(a.X+a.Width, b.X+b.Width), min(a.Y+a.Height, b.Y+b.Height)
	if right <= x || bottom <= y {
		return CaptchaRect{}, false
	}
	return CaptchaRect{X: x, Y: y, Width: right - x, Height: bottom - y}, true
}

// evaluateCaptchaFrameBox measures the child frame's content origin in its
// parent viewport. Only the target root needs document scroll added: nested
// iframe offsets are already relative to their scrolled parent viewport.
func (c *Client) evaluateCaptchaFrameBox(ctx context.Context, session, parentFrameID, childFrameID string, rootParent bool) (CaptchaRect, bool) {
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
	function := "function(){const r=this.getBoundingClientRect();return {x:r.x+this.clientLeft,y:r.y+this.clientTop,width:this.clientWidth,height:this.clientHeight};}"
	if rootParent {
		function = "function(){const r=this.getBoundingClientRect();return {x:r.x+this.clientLeft+window.scrollX,y:r.y+this.clientTop+window.scrollY,width:this.clientWidth,height:this.clientHeight};}"
	}
	var box runtimeResult
	if err := c.Call(ctx, session, "Runtime.callFunctionOn", map[string]any{
		"objectId":            resolved.Object.ObjectID,
		"functionDeclaration": function,
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
	return c.scanCaptchaPages(ctx, true)
}

// CaptchaChallenges detects widgets without taking screenshots.
func (c *Client) CaptchaChallenges(ctx context.Context) ([]CaptchaChallenge, error) {
	captures, err := c.scanCaptchaPages(ctx, false)
	if err != nil {
		return nil, err
	}
	challenges := make([]CaptchaChallenge, 0, len(captures))
	for _, capture := range captures {
		challenges = append(challenges, capture.Challenge)
	}
	return challenges, nil
}

func (c *Client) scanCaptchaPages(ctx context.Context, screenshot bool) ([]CaptchaCapture, error) {
	var targets struct {
		Infos []captchaTargetInfo `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return nil, err
	}
	captures := []CaptchaCapture{}
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
		capture, found, err := c.captchaCaptureForTarget(ctx, session, target, targets.Infos, screenshot)
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
		if err != nil {
			if errors.Is(err, errBrowserOperationRejected) {
				// A target can navigate or close while it is being probed.
				continue
			}
			return nil, err
		}
		if !found {
			continue
		}
		captures = append(captures, capture)
	}
	return captures, nil
}

func (c *Client) captchaCaptureForTarget(ctx context.Context, session string, target captchaTargetInfo, targets []captchaTargetInfo, screenshot bool) (CaptchaCapture, bool, error) {
	var tree struct {
		Tree captchaFrameNode `json:"frameTree"`
	}
	if err := c.Call(ctx, session, "Page.getFrameTree", nil, &tree); err != nil {
		return CaptchaCapture{}, false, err
	}
	rootScroll, err := c.captchaFrameScroll(ctx, session, tree.Tree.Frame.ID)
	if err != nil {
		return CaptchaCapture{}, false, err
	}
	seen := map[string]bool{target.ID: true}
	detail, err := c.findCaptchaInTarget(ctx, session, tree.Tree, rootScroll, rootScroll, nil, targets, seen, screenshot)
	if err != nil {
		return CaptchaCapture{}, false, err
	}
	if detail == nil || detail.Type == "" {
		return CaptchaCapture{}, false, nil
	}
	capture := CaptchaCapture{Challenge: CaptchaChallenge{URL: target.URL, Type: detail.Type, SiteKey: detail.Sitekey, TargetID: target.ID}, SolverPNG: detail.SolverPNG}
	if screenshot && detail.Type == "image" && len(detail.SolverPNG) > 0 {
		// The decoded image is both more reliable and safer than a page crop:
		// transparent image pixels cannot reveal content underneath them.
		capture.PNG = detail.SolverPNG
		return capture, true, nil
	}
	if x, y, width, height, ok := clipCaptchaRect(detail.Rect); ok && screenshot {
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

// findCaptchaInTarget probes one target, then recurses into its out-of-process
// iframe targets. targetOffset is the target viewport's origin in page document
// coordinates; targetScroll converts document rectangles back to that viewport.
func (c *Client) findCaptchaInTarget(ctx context.Context, session string, root captchaFrameNode, targetOffset, targetScroll CaptchaRect, targetBounds *CaptchaRect, targets []captchaTargetInfo, seen map[string]bool, screenshot bool) (*CaptchaDetail, error) {
	detail, frameID, err := c.verifyCaptchaDetail(ctx, session, root)
	if err != nil {
		return nil, err
	}
	if detail != nil && detail.Type != "" {
		if screenshot && detail.Type == "image" {
			detail.SolverPNG = c.captchaSolverImage(ctx, session, frameID)
		}
		if detail.Rect != nil {
			detail.Rect.X += targetOffset.X - targetScroll.X
			detail.Rect.Y += targetOffset.Y - targetScroll.Y
			if targetBounds != nil {
				visible, ok := intersectCaptchaRect(*detail.Rect, *targetBounds)
				if ok {
					detail.Rect = &visible
				} else {
					detail.Rect = nil
				}
			}
		}
		if _, _, _, _, visible := clipCaptchaRect(detail.Rect); !visible {
			detail.SolverPNG = nil
		}
		return detail, nil
	}
	frameIDs := map[string]bool{}
	var collect func(captchaFrameNode)
	collect = func(node captchaFrameNode) {
		frameIDs[node.Frame.ID] = true
		for _, child := range node.ChildFrames {
			collect(child)
		}
	}
	collect(root)
	for _, child := range targets {
		if child.Type != "iframe" || seen[child.ID] || !frameIDs[child.ParentFrameID] {
			continue
		}
		seen[child.ID] = true
		origin, ok := c.captchaFrameContentOrigin(ctx, session, root, child.ParentFrameID, child.ID)
		if !ok {
			continue
		}
		var attached struct {
			Session string `json:"sessionId"`
		}
		if err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": child.ID, "flatten": true}, &attached); err != nil {
			continue
		}
		childOffset := CaptchaRect{X: targetOffset.X + origin.X - targetScroll.X, Y: targetOffset.Y + origin.Y - targetScroll.Y}
		childBounds := CaptchaRect{X: childOffset.X, Y: childOffset.Y, Width: origin.Width, Height: origin.Height}
		if targetBounds != nil {
			visible, ok := intersectCaptchaRect(childBounds, *targetBounds)
			if !ok {
				_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": attached.Session}, nil)
				continue
			}
			childBounds = visible
		}
		found, err := c.findCaptchaInAttachedTarget(ctx, attached.Session, childOffset, &childBounds, targets, seen, screenshot)
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": attached.Session}, nil)
		if err != nil {
			continue // A navigating iframe must not abort scans of other frames.
		}
		if found != nil {
			return found, nil
		}
	}
	return nil, nil
}

func (c *Client) findCaptchaInAttachedTarget(ctx context.Context, session string, offset CaptchaRect, bounds *CaptchaRect, targets []captchaTargetInfo, seen map[string]bool, screenshot bool) (*CaptchaDetail, error) {
	var tree struct {
		Tree captchaFrameNode `json:"frameTree"`
	}
	if err := c.Call(ctx, session, "Page.getFrameTree", nil, &tree); err != nil {
		return nil, err
	}
	scroll, err := c.captchaFrameScroll(ctx, session, tree.Tree.Frame.ID)
	if err != nil {
		return nil, err
	}
	return c.findCaptchaInTarget(ctx, session, tree.Tree, offset, scroll, bounds, targets, seen, screenshot)
}

func (c *Client) captchaFrameContentOrigin(ctx context.Context, session string, root captchaFrameNode, parentFrameID, childFrameID string) (CaptchaRect, bool) {
	box, ok := c.evaluateCaptchaFrameBox(ctx, session, parentFrameID, childFrameID, parentFrameID == root.Frame.ID)
	if !ok {
		return CaptchaRect{}, false
	}
	if parentFrameID != root.Frame.ID {
		chain := frameChain(root, parentFrameID)
		if len(chain) < 2 {
			return CaptchaRect{}, false
		}
		for i := 0; i+1 < len(chain); i++ {
			parent, ok := c.evaluateCaptchaFrameBox(ctx, session, chain[i], chain[i+1], i == 0)
			if !ok {
				return CaptchaRect{}, false
			}
			box.X += parent.X
			box.Y += parent.Y
		}
	}
	return box, true
}

func (c *Client) captchaFrameScroll(ctx context.Context, session, frameID string) (CaptchaRect, error) {
	var world struct {
		ID int64 `json:"executionContextId"`
	}
	if err := c.Call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": frameID, "worldName": "vmbox-captcha-probe"}, &world); err != nil {
		return CaptchaRect{}, err
	}
	var result runtimeResult
	if err := c.Call(ctx, session, "Runtime.evaluate", map[string]any{"contextId": world.ID, "expression": "({x:window.scrollX,y:window.scrollY})", "returnByValue": true}, &result); err != nil {
		return CaptchaRect{}, err
	}
	var scroll CaptchaRect
	if len(result.Exception) > 0 || json.Unmarshal(result.Result.Value, &scroll) != nil {
		return CaptchaRect{}, fmt.Errorf("captcha frame scroll unavailable")
	}
	return scroll, nil
}

// CaptchaAnswer carries the owner's solution from the chat UI to the managed
// browser. Widget challenges (reCAPTCHA, hCaptcha, Turnstile) bring a response
// token the chat's embedded widget produced; image challenges bring the typed
// answer. TargetID and PageURL pin injection to the captured tab. Token-like secrets
// exist only inside this box and are never returned.
type CaptchaAnswer struct {
	Type     string `json:"type"`
	Token    string `json:"token,omitempty"`
	Text     string `json:"text,omitempty"`
	PageURL  string `json:"pageUrl,omitempty"`
	TargetID string `json:"targetId,omitempty"`
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
		Infos []captchaTargetInfo `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return err
	}
	applied := false
	var relatedFrames map[string]bool
	for _, target := range targets.Infos {
		if target.Type != "page" {
			continue
		}
		parsed, err := url.Parse(target.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		// Only inject into the page the card was captured from, so a token can
		// never land on an unrelated tab that happens to show the same type.
		if (answer.TargetID != "" && answer.TargetID != target.ID) || (answer.PageURL != "" && !sameCaptchaPage(answer.PageURL, target.URL)) {
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
		if found && err == nil {
			relatedFrames = c.captchaRelatedFrameIDs(ctx, session, targets.Infos)
		}
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
		if err != nil {
			return err
		}
		if found {
			applied = true
			break
		}
	}
	if !applied {
		return fmt.Errorf("no matching captcha challenge is currently open in the managed browser")
	}
	// The token also has to land inside the provider's own frame: reCAPTCHA and
	// hCaptcha store the response there. Out-of-process widget frames appear as
	// their own targets under site isolation; best effort when they do not.
	for _, widget := range targets.Infos {
		if widget.Type != "iframe" || !relatedFrames[widget.ParentFrameID] || captchaProviderFrame(answer.Type, widget.URL) != "widget" {
			continue
		}
		var attached struct {
			Session string `json:"sessionId"`
		}
		if err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": widget.ID, "flatten": true}, &attached); err != nil {
			continue
		}
		_, _ = c.evaluateCaptchaSnippet(ctx, attached.Session, captchaWidgetSnippet(answer.Type, jsonQuote(answer.Token)))
		_ = c.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": attached.Session}, nil)
	}
	return nil
}

// sameCaptchaPage compares the card's captured URL with an open target. The
// complete URL must still match; query parameters often identify the challenge.
// An unpinned answer (no card URL) may target any open page.
func sameCaptchaPage(cardURL, targetURL string) bool {
	if cardURL == "" {
		return true
	}
	return cardURL == targetURL
}

func (c *Client) captchaRelatedFrameIDs(ctx context.Context, session string, targets []captchaTargetInfo) map[string]bool {
	var tree struct {
		Tree captchaFrameNode `json:"frameTree"`
	}
	if c.Call(ctx, session, "Page.getFrameTree", nil, &tree) != nil {
		return nil
	}
	frames := map[string]bool{}
	var collect func(captchaFrameNode)
	collect = func(node captchaFrameNode) {
		frames[node.Frame.ID] = true
		for _, child := range node.ChildFrames {
			collect(child)
		}
	}
	collect(tree.Tree)
	for changed := true; changed; {
		changed = false
		for _, target := range targets {
			if target.Type == "iframe" && frames[target.ParentFrameID] && !frames[target.ID] {
				frames[target.ID] = true
				changed = true
			}
		}
	}
	return frames
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
