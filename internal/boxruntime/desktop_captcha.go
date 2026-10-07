package boxruntime

import (
	"context"
	"strings"

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
		"\n\nDo not attempt to solve it. Take a screenshot (take_screenshot) and ask the owner over chat to solve the challenge in the browser, then continue once it is gone."
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
}
