package controller

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const boxMessageCaptchaPrefix = "vmbox-captcha:"

type boxMessageCaptchaEnvelope struct {
	Text    string               `json:"text"`
	Captcha v1.BoxMessageCaptcha `json:"captcha"`
}

// encodeBoxMessageCaptcha hides the captcha card payload inside the stored
// message text, the same way structured questions are persisted. The display
// text stays readable for older clients and push notifications.
func encodeBoxMessageCaptcha(text string, captcha v1.BoxMessageCaptcha) string {
	data, _ := json.Marshal(boxMessageCaptchaEnvelope{Text: text, Captcha: captcha})
	return boxMessageCaptchaPrefix + base64.RawURLEncoding.EncodeToString(data)
}

// decodeBoxMessageCaptcha restores the extracted captcha data and the display
// text of a captcha card message. Any other message is left untouched.
func decodeBoxMessageCaptcha(message *v1.BoxMessage) {
	if !strings.HasPrefix(message.Text, boxMessageCaptchaPrefix) {
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(message.Text, boxMessageCaptchaPrefix))
	if err != nil {
		return
	}
	var envelope boxMessageCaptchaEnvelope
	if json.Unmarshal(data, &envelope) != nil || strings.TrimSpace(envelope.Captcha.Type) == "" {
		return
	}
	message.Captcha = &envelope.Captcha
	message.Text = envelope.Text
}
