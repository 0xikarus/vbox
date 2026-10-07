package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/browser"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// firstChatEventPNG returns the first PNG attachment's bytes; image captchas
// are solved from their capture.
func firstChatEventPNG(images []boxruntime.ChatEventImage) []byte {
	for _, image := range images {
		if image.MediaType != "image/png" {
			continue
		}
		if png, err := base64.StdEncoding.DecodeString(image.Data); err == nil && len(png) > 0 {
			return png
		}
	}
	return nil
}

// autoSolveCaptcha solves a freshly posted captcha message with the account's
// 2captcha credential when the owner enabled that. It runs after the captcha
// card is stored, so the owner can still solve it manually; the first attempt
// wins because the runtime only accepts an answer while a challenge is open.
// The API key and the returned token never enter chat or agent context.
func (s *Server) autoSolveCaptcha(accountID string, task v1.BoxTask, prov provider.Provider, messageID string, captcha v1.BoxMessageCaptcha, png []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	setting, apiKey, err := s.Store.CaptchaSolver(ctx, accountID)
	if err != nil {
		s.Logger.Warn("captcha solver setting unavailable", "account", accountID, "error", err)
		return
	}
	if !setting.Enabled || apiKey == "" {
		return
	}
	solver := twoCaptchaSolver{Client: s.HTTP}
	answer, err := solver.Solve(ctx, apiKey, captcha, png)
	if err != nil {
		s.postAutoSolveOutcome(ctx, accountID, task, messageID, fmt.Sprintf("Automatic captcha solving failed: %v. Please solve it in chat.", err))
		return
	}
	answerPayload := browser.CaptchaAnswer{Type: captcha.Type, Token: answer, PageURL: captcha.URL}
	if captcha.Type == "image" {
		answerPayload = browser.CaptchaAnswer{Type: "image", Text: answer, PageURL: captcha.URL}
	}
	if err := s.applyCaptchaAnswer(ctx, accountID, prov, task.LogicalBoxID, answerPayload); err != nil {
		s.postAutoSolveOutcome(ctx, accountID, task, messageID, fmt.Sprintf("Automatic captcha solving failed: %v. Please solve it in chat.", err))
		return
	}
	s.postAutoSolveOutcome(ctx, accountID, task, messageID, "Captcha solved automatically with 2captcha ("+captcha.Type+").")
}

// applyCaptchaAnswer injects the answer into the currently assigned worker's
// browser, rechecking the assignment fence like the owner's manual route.
func (s *Server) applyCaptchaAnswer(ctx context.Context, accountID string, prov provider.Provider, boxID string, answer browser.CaptchaAnswer) error {
	a, err := s.Store.assignment(ctx, accountID, boxID)
	if err != nil || a.Box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("box is offline")
	}
	payload, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("answer could not be encoded")
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "captcha-answer", nativeFence(a)}, provider.ExecOptions{Stdin: bytes.NewReader(payload), Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("the challenge is no longer open or the answer was rejected")
	}
	return nil
}

// postAutoSolveOutcome appends a short note to the box conversation so the
// owner sees what happened without watching the browser.
func (s *Server) postAutoSolveOutcome(ctx context.Context, accountID string, task v1.BoxTask, messageID, text string) {
	note := fmt.Sprintf("Automatic captcha outcome (%s): %s", task.BoxName, text)
	if _, _, err := s.Store.InsertAgentBoxMessage(ctx, accountID, task.ID, "captcha-auto:"+messageID, note); err != nil {
		s.Logger.Warn("could not record captcha auto-solve outcome", "account", accountID, "error", err)
		return
	}
	s.pushAgentReply(ctx, accountID, task, note)
}
