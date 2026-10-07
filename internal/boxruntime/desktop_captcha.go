package boxruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/browser"
)

// callDetectCaptcha reports challenge widgets in the managed browser without
// reading their inputs. The agent is expected to hand the challenge to its
// owner, never to solve it automatically.
func callDetectCaptcha(ctx context.Context, assignment string) (map[string]any, error) {
	var challenges []browser.CaptchaChallenge
	err := withDesktopBrowser(ctx, assignment, func(client *browser.Client, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		found, err := client.CaptchaChallenges(ctx)
		if err != nil {
			return err
		}
		challenges = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(challenges) == 0 {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "No CAPTCHA challenge detected in the managed browser's open pages."}}}, nil
	}
	var lines []string
	for _, challenge := range challenges {
		lines = append(lines, "- "+challenge.Type+" at "+challenge.URL)
	}
	text := "CAPTCHA challenge detected:\n" + strings.Join(lines, "\n") +
		"\n\nDo not attempt to solve it. Call show_captcha to capture the challenge and show it to the owner in chat, then continue once it is gone."
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
}

// callShowCaptcha posts the captcha card to the owner's chat. solve_captcha
// routes here too: the card is the handoff, and the controller tasks the owner
// with solving it (handling the answer automatically when configured; either
// way it is injected into the managed browser, and the agent never sees a
// token). It never reads solver inputs and never solves or submits anything.
func callShowCaptcha(ctx context.Context, assignment string) (map[string]any, error) {
	var captures []browser.CaptchaCapture
	err := withDesktopBrowser(ctx, assignment, func(client *browser.Client, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		found, err := client.CaptchaCaptures(ctx)
		if err != nil {
			return err
		}
		captures = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(captures) == 0 {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "No CAPTCHA challenge detected in the managed browser's open pages; nothing was sent to chat."}}}, nil
	}
	var images []ChatEventImage
	var lines []string
	var card *ChatEventCaptcha
	for index, capture := range captures {
		lines = append(lines, "- "+capture.Challenge.Type+" at "+capture.Challenge.URL)
		// A hidden or zero-size widget has no pixels to show; the card still
		// carries its data, but an empty attachment would be rejected by the
		// controller and stall the outbox.
		if len(capture.PNG) == 0 {
			continue
		}
		name := fmt.Sprintf("captcha-%s-%d.png", capture.Challenge.Type, index+1)
		images = append(images, ChatEventImage{Name: name, MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(capture.PNG)})
	}
	first := captures[0].Challenge
	text := "CAPTCHA challenge detected in the managed browser (" + strings.Join(lines, "; ") + "). Please solve it from the captcha card in chat; this agent continues once the challenge is gone."
	card = &ChatEventCaptcha{Type: first.Type, URL: first.URL, SiteKey: first.SiteKey}
	if err := writeDesktopChatEvent(ctx, assignment, ChatEvent{Kind: "reply", Text: text, Images: images, Captcha: card}); err != nil {
		return nil, err
	}
	content := []map[string]any{{"type": "text", "text": fmt.Sprintf("Captured %d CAPTCHA challenge(s) using the add-on's extracted data and posted a captcha card to the owner's chat:\n%s\n\nThe owner was tasked with solving it. Do not attempt to solve it. Wait for the answer to arrive, then continue once the challenge is gone.", len(captures), strings.Join(lines, "\n"))}}
	for _, capture := range captures {
		if len(capture.PNG) == 0 {
			continue
		}
		content = append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(capture.PNG)})
	}
	return map[string]any{"content": content}, nil
}

// DecodeCaptchaAnswer parses the controller's solution payload. Tokens are
// transit secrets: they exist only inside this box and are never logged.
func DecodeCaptchaAnswer(input []byte) (browser.CaptchaAnswer, error) {
	var answer browser.CaptchaAnswer
	if err := json.Unmarshal(input, &answer); err != nil {
		return browser.CaptchaAnswer{}, fmt.Errorf("invalid captcha answer")
	}
	return answer, answer.Validate()
}

// SubmitCaptchaAnswer applies the owner's solution inside the desktop input
// fence and waits briefly so the page can verify the answer before the
// controller takes its confirmation screenshot.
func SubmitCaptchaAnswer(ctx context.Context, assignment string, answer browser.CaptchaAnswer) error {
	return withDesktopBrowser(ctx, assignment, func(client *browser.Client, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		if err := client.SubmitCaptchaAnswer(ctx, answer); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		return check()
	})
}
