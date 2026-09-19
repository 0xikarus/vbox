package controller

import (
	"os"
	"strings"
	"testing"
)

func TestChatDeleteNeedsClickNotTypedName(t *testing.T) {
	html, err := os.ReadFile("web/chat.html")
	if err != nil { t.Fatal(err) }
	js, err := os.ReadFile("web/chat.js")
	if err != nil { t.Fatal(err) }
	if strings.Contains(string(html), `name="confirmation"`) {
		t.Fatal("chat delete must not require typing the box name")
	}
	if !strings.Contains(string(js), `confirmation:deleteTarget.name`) {
		t.Fatal("delete request must still carry the server-required box name")
	}
}
