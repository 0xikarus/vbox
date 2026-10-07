package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// twoCaptchaSolver submits a detected challenge to the owner-configured 2captcha
// account and polls until the human solver returns a token or text answer. It
// is opt-in per account: without a stored API key it is never used.
type twoCaptchaSolver struct {
	Client  *http.Client
	BaseURL string
}

func (s twoCaptchaSolver) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return "https://2captcha.com"
}

var (
	twoCaptchaPollInterval = 5 * time.Second
	twoCaptchaMaxWait      = 150 * time.Second
)

// captchaTaskName maps a detected challenge type to 2captcha's task method.
func captchaTaskName(kind string) string {
	switch kind {
	case "recaptcha":
		return "userrecaptcha"
	case "hcaptcha":
		return "hcaptcha"
	case "turnstile":
		return "turnstile"
	case "image":
		return "base64"
	}
	return ""
}

// captchaSubmitValues builds the in.php parameters for one challenge. Widget
// tasks always carry the page URL: 2captcha requires it for reCAPTCHA,
// hCaptcha, and Turnstile.
func captchaSubmitValues(kind, siteKey, pageURL string, png []byte) (url.Values, error) {
	values := url.Values{}
	switch kind {
	case "recaptcha":
		if siteKey == "" || pageURL == "" {
			return nil, fmt.Errorf("reCAPTCHA site key or page URL unavailable")
		}
		values.Set("method", "userrecaptcha")
		values.Set("googlekey", siteKey)
		values.Set("pageurl", pageURL)
	case "hcaptcha":
		if siteKey == "" || pageURL == "" {
			return nil, fmt.Errorf("hCaptcha site key or page URL unavailable")
		}
		values.Set("method", "hcaptcha")
		values.Set("sitekey", siteKey)
		values.Set("pageurl", pageURL)
	case "turnstile":
		if siteKey == "" || pageURL == "" {
			return nil, fmt.Errorf("Turnstile site key or page URL unavailable")
		}
		values.Set("method", "turnstile")
		values.Set("sitekey", siteKey)
		values.Set("pageurl", pageURL)
	case "image":
		if len(png) == 0 || len(png) > 3<<20 {
			return nil, fmt.Errorf("no usable challenge capture for the image solver")
		}
		values.Set("method", "base64")
		values.Set("body", base64.StdEncoding.EncodeToString(png))
		if pageURL != "" {
			values.Set("pageurl", pageURL)
		}
	default:
		return nil, fmt.Errorf("unsupported captcha type %q", kind)
	}
	return values, nil
}

// Solve returns the solver's response token for widget challenges, or the read
// answer text for image challenges.
func (s twoCaptchaSolver) Solve(ctx context.Context, apiKey string, captcha v1.BoxMessageCaptcha, png []byte) (string, error) {
	values, err := captchaSubmitValues(captcha.Type, captcha.SiteKey, captcha.URL, png)
	if err != nil {
		return "", err
	}
	values.Set("key", apiKey)
	values.Set("json", "1")
	submitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var submitted struct {
		Status  int    `json:"status"`
		Request string `json:"request"`
	}
	if err := s.postForm(submitCtx, s.base()+"/in.php", values, &submitted); err != nil {
		return "", err
	}
	if submitted.Status != 1 || submitted.Request == "" {
		return "", fmt.Errorf("2captcha rejected the task: %s", twoCaptchaErrorText(submitted.Request))
	}
	taskID := submitted.Request
	deadline := time.Now().Add(twoCaptchaMaxWait)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(twoCaptchaPollInterval):
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("2captcha did not return an answer in time")
		}
		pollCtx, pollCancel := context.WithTimeout(ctx, 30*time.Second)
		var polled struct {
			Status  int    `json:"status"`
			Request string `json:"request"`
		}
		err := s.postForm(pollCtx, s.base()+"/res.php", url.Values{
			"key":    {apiKey},
			"action": {"get"},
			"id":     {taskID},
			"json":   {"1"},
		}, &polled)
		pollCancel()
		if err != nil {
			continue
		}
		if polled.Status == 1 && polled.Request != "" {
			return polled.Request, nil
		}
		request := strings.ToUpper(polled.Request)
		if request == "CAPCHA_NOT_READY" {
			continue
		}
		if strings.HasPrefix(request, "ERROR_") {
			return "", fmt.Errorf("2captcha failed: %s", twoCaptchaErrorText(polled.Request))
		}
	}
}

// twoCaptchaErrorText turns ERROR_BAD_KEY style codes into readable text.
func twoCaptchaErrorText(code string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(code, "ERROR_"), "_", " "))
}

func (s twoCaptchaSolver) postForm(ctx context.Context, endpoint string, values url.Values, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 35 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("2captcha is unreachable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || response.StatusCode >= 300 {
		return fmt.Errorf("2captcha returned status %d", response.StatusCode)
	}
	if json.Unmarshal(body, result) != nil {
		return fmt.Errorf("2captcha returned an unreadable response")
	}
	return nil
}
