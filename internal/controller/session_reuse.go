package controller

import (
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func reusableInteractiveSession(sessions []v1.Session, preferred string) string {
	selected := ""
	for _, session := range sessions {
		if session.Name == "" || session.Partial || strings.HasPrefix(session.Name, "task-") || session.Name == "vmbox-desktop" || strings.HasPrefix(session.Name, "vmbox-internal-") {
			continue
		}
		if session.Name == preferred {
			return session.Name
		}
		if selected == "" || session.Name < selected {
			selected = session.Name
		}
	}
	return selected
}
