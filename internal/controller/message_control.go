package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const boxMessageControlPrefix = "vmbox-control:"

func encodeBoxMessageControl(control v1.BoxMessageControl) string {
	data, _ := json.Marshal(control)
	return boxMessageControlPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeBoxMessageControl(message *v1.BoxMessage) {
	if !strings.HasPrefix(message.Text, boxMessageControlPrefix) {
		return
	}
	encoded := strings.TrimPrefix(message.Text, boxMessageControlPrefix)
	message.Text = "Remote control notice unavailable."
	if len(encoded) > 4096 {
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	var control v1.BoxMessageControl
	if json.Unmarshal(data, &control) != nil || control.Kind != "remote_control" || control.ActorBoxID == "" || control.ActorName == "" || len(control.ActorName) > 100 || control.Actions < 1 || control.FirstAt.IsZero() || control.LastAt.Before(control.FirstAt) {
		return
	}
	message.Control = &control
	message.Text = fmt.Sprintf("Controlled by %s · %d actions", control.ActorName, control.Actions)
}
