package notifications

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestTelegramDefaultCoworkerRequiresMappedAllowlistedSender(t *testing.T) {
	for _, tc := range []struct {
		name       string
		user, chat int64
		mapped     bool
		want       bool
	}{
		{"allowed", 7, 9, true, true}, {"foreign user", 8, 9, true, false}, {"foreign chat", 7, 10, true, false}, {"unmapped", 7, 9, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			adapter := Telegram{DefaultCoworker: "coworker-telegram", AllowedUsers: map[int64]bool{7: true}, AllowedChats: map[int64]bool{9: true}, UserMap: map[string]string{}}
			if tc.mapped {
				adapter.UserMap["7"] = "owner"
			}
			adapter.CoworkerCommand = func(_ context.Context, user string, update, chat int64, text string) error {
				called = true
				if user != "owner" || update != 42 || chat != 9 || text != "/coworker coworker-telegram Hello Grüße" {
					t.Fatal("message routing changed")
				}
				return nil
			}
			body := `{"update_id":42,"message":{"from":{"id":USER},"chat":{"id":CHAT},"text":"Hello Grüße"}}`
			body = strings.ReplaceAll(body, "USER", strconv.FormatInt(tc.user, 10))
			body = strings.ReplaceAll(body, "CHAT", strconv.FormatInt(tc.chat, 10))
			err := adapter.HandleUpdate(context.Background(), strings.NewReader(body))
			if called != tc.want || (err == nil) != tc.want {
				t.Fatalf("called=%t error=%v", called, err)
			}
		})
	}
}
