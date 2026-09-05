package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/notifications"
)

type integrationAnswerer struct{ store *Store }

func (a integrationAnswerer) Answer(ctx context.Context, accountID, userID, questionID, answer string) error {
	principal, err := a.store.IntegrationPrincipal(ctx, accountID, userID)
	if err != nil {
		return err
	}
	return a.store.Answer(ctx, principal, questionID, answer)
}

func (s *Server) notificationInbound(w http.ResponseWriter, r *http.Request) {
	kind, accountID, name := r.PathValue("kind"), r.PathValue("account"), r.PathValue("name")
	value, err := s.Store.Notification(r.Context(), accountID, kind, name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("notification destination not found"))
		return
	}
	var secret, config map[string]any
	if err := json.Unmarshal(value.Secret, &secret); err != nil || json.Unmarshal(value.Config, &config) != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("invalid notification configuration"))
		return
	}
	answerer := integrationAnswerer{store: s.Store}
	switch kind {
	case "telegram":
		expected, _ := secret["webhookSecret"].(string)
		provided := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if expected == "" || len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid Telegram webhook secret"))
			return
		}
		adapter := telegramInbound(value, config, answerer)
		adapter.CoworkerCommand = s.telegramCoworkerCommand(value, secret)
		err = adapter.HandleUpdate(r.Context(), r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "discord":
		publicKey, err := hex.DecodeString(stringField(secret, "publicKey"))
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("invalid Discord public key"))
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			if err == nil {
				err = fmt.Errorf("Discord request is too large")
			}
			writeError(w, http.StatusBadRequest, err)
			return
		}
		adapter := discordInbound(value, config, publicKey, answerer)
		response, err := adapter.HandleInteraction(r.Context(), r.Header.Get("X-Signature-Ed25519"), r.Header.Get("X-Signature-Timestamp"), body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unsupported notification integration"))
	}
}

func telegramInbound(value DecryptedNotification, config map[string]any, answerer notifications.Answerer) notifications.Telegram {
	users, chats := map[int64]bool{}, map[int64]bool{}
	for _, item := range value.AllowedUsers {
		if id, err := strconv.ParseInt(item, 10, 64); err == nil {
			users[id] = true
		}
	}
	for _, item := range value.AllowedChats {
		if id, err := strconv.ParseInt(item, 10, 64); err == nil {
			chats[id] = true
		}
	}
	return notifications.Telegram{AllowedUsers: users, AllowedChats: chats, Answerer: answerer, AccountID: value.AccountID, UserMap: stringMap(config["userMap"]), DefaultCoworker: stringField(config, "defaultCoworker")}
}

func discordInbound(value DecryptedNotification, config map[string]any, publicKey ed25519.PublicKey, answerer notifications.Answerer) notifications.Discord {
	users, guilds, channels := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range value.AllowedUsers {
		users[item] = true
	}
	for _, item := range value.AllowedChats {
		channels[item] = true
	}
	for _, item := range stringSlice(config["allowedGuilds"]) {
		guilds[item] = true
	}
	return notifications.Discord{PublicKey: publicKey, AllowedUsers: users, AllowedGuilds: guilds, AllowedChannels: channels, Answerer: answerer, AccountID: value.AccountID, UserMap: stringMap(config["userMap"])}
}

func validateInteractiveNotification(kind string, req v1.PutNotificationRequest) error {
	if kind != "telegram" && kind != "discord" {
		return nil
	}
	var secret, config map[string]any
	if json.Unmarshal(req.Secret, &secret) != nil || json.Unmarshal(req.Config, &config) != nil {
		return fmt.Errorf("invalid notification secret or config")
	}
	if kind == "telegram" && (stringField(secret, "token") == "" || stringField(secret, "webhookSecret") == "") {
		return fmt.Errorf("Telegram requires token and webhookSecret")
	}
	if box := stringField(config, "defaultCoworker"); box != "" && (len(box) > 128 || strings.ContainsAny(box, " \t\r\n")) {
		return fmt.Errorf("defaultCoworker must be a box name or ID without whitespace")
	}
	if kind == "discord" {
		key, err := hex.DecodeString(stringField(secret, "publicKey"))
		if stringField(secret, "webhookUrl") == "" || err != nil || len(key) != ed25519.PublicKeySize {
			return fmt.Errorf("Discord requires webhookUrl and a valid Ed25519 publicKey")
		}
	}
	mapping := stringMap(config["userMap"])
	for _, user := range req.AllowedUsers {
		if mapping[user] == "" {
			return fmt.Errorf("allowed user %q must map to a controller user in config.userMap", user)
		}
	}
	return nil
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func stringMap(value any) map[string]string {
	result := map[string]string{}
	values, _ := value.(map[string]any)
	for key, item := range values {
		if text, ok := item.(string); ok {
			result[key] = text
		}
	}
	return result
}

func stringSlice(value any) []string {
	var result []string
	values, _ := value.([]any)
	for _, item := range values {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
