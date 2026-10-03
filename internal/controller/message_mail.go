package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const boxMessageMailPrefix = "vmbox-mail:"

func encodeBoxMessageMail(mail v1.BoxMessageMail) string {
	data, _ := json.Marshal(mail)
	return boxMessageMailPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeBoxMessageMail(message *v1.BoxMessage) {
	if !strings.HasPrefix(message.Text, boxMessageMailPrefix) {
		return
	}
	encoded := strings.TrimPrefix(message.Text, boxMessageMailPrefix)
	message.Text = "Mail notice unavailable."
	if len(encoded) > 32768 {
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	var mail v1.BoxMessageMail
	if json.Unmarshal(data, &mail) != nil {
		return
	}
	switch mail.Kind {
	case "mail_batch":
		if len(mail.Items) == 0 || len(mail.Items) > 20 || mail.More < 0 {
			return
		}
		for _, item := range mail.Items {
			if item.ID == "" || len(item.From) > 254 || len(item.FromName) > 200 || len(item.Subject) > 200 || len(item.Preview) > 280 {
				return
			}
		}
		message.Text = fmt.Sprintf("✉ %d new mail", len(mail.Items))
		if len(mail.Items) != 1 {
			message.Text += "s"
		}
		if mail.More > 0 {
			message.Text += fmt.Sprintf(" · %d more", mail.More)
		}
	case "outbox_status":
		if mail.OutboxID == "" || len(mail.To) > 500 || len(mail.Subject) > 200 || len(mail.Reason) > 500 {
			return
		}
		switch mail.Status {
		case "pending_approval":
			message.Text = "Draft pending approval"
		case "sent":
			message.Text = "Email sent"
		case "rejected":
			message.Text = "Draft rejected"
		case "failed":
			message.Text = "Email send failed"
		default:
			return
		}
	default:
		return
	}
	message.Mail = &mail
}
