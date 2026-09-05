package notifications

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type Delivery struct {
	AccountID  string      `json:"accountId"`
	RunID      string      `json:"runId"`
	Box        string      `json:"box"`
	Event      string      `json:"event"`
	State      v1.JobState `json:"state"`
	Message    string      `json:"message"`
	QuestionID string      `json:"questionId,omitempty"`
	Cost       string      `json:"cost,omitempty"`
}

type Adapter interface {
	Name() string
	Send(context.Context, Delivery) error
}
type Answerer interface {
	Answer(context.Context, string, string, string, string) error
}

type Dispatcher struct {
	Adapters []Adapter
	Attempts int
}

func (d Dispatcher) Send(ctx context.Context, value Delivery) map[string]error {
	attempts := d.Attempts
	if attempts < 1 {
		attempts = 4
	}
	result := make(map[string]error)
	for _, adapter := range d.Adapters {
		var err error
		for attempt := 0; attempt < attempts; attempt++ {
			if err = adapter.Send(ctx, value); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
				attempt = attempts
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		if err != nil {
			result[adapter.Name()] = err
		}
	}
	return result
}

type Webhook struct {
	URL    string
	Secret []byte
	Client *http.Client
}

func (Webhook) Name() string { return "webhook" }
func (w Webhook) Send(ctx context.Context, value Delivery) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if len(w.Secret) > 0 {
		mac := hmac.New(sha256.New, w.Secret)
		_, _ = mac.Write(data)
		req.Header.Set("X-VMBox-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return send(w.Client, req)
}

type Discord struct {
	WebhookURL      string
	PublicKey       ed25519.PublicKey
	AllowedUsers    map[string]bool
	AllowedGuilds   map[string]bool
	AllowedChannels map[string]bool
	Client          *http.Client
	Answerer        Answerer
	AccountID       string
	UserMap         map[string]string
}

func (Discord) Name() string { return "discord" }
func (d Discord) Send(ctx context.Context, value Delivery) error {
	body := map[string]any{"content": fmt.Sprintf("vmbox **%s** · **%s**\n%s", value.Box, value.State, value.Message), "allowed_mentions": map[string]any{"parse": []string{}}}
	if value.QuestionID != "" {
		body["components"] = []any{map[string]any{"type": 1, "components": []any{map[string]any{"type": 2, "style": 1, "label": "Answer", "custom_id": "answer:" + value.QuestionID}}}}
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return send(d.Client, req)
}
func (d Discord) HandleInteraction(ctx context.Context, signature, timestamp string, body []byte) (map[string]any, error) {
	sig, err := hex.DecodeString(signature)
	if err != nil || !ed25519.Verify(d.PublicKey, append([]byte(timestamp), body...), sig) {
		return nil, fmt.Errorf("invalid Discord interaction signature")
	}
	var interaction struct {
		Type      int    `json:"type"`
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
		Member    struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"member"`
		Data struct {
			CustomID   string `json:"custom_id"`
			Components []struct {
				Components []struct {
					Value string `json:"value"`
				} `json:"components"`
			} `json:"components"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &interaction); err != nil {
		return nil, err
	}
	if interaction.Type == 1 {
		return map[string]any{"type": 1}, nil
	}
	user := interaction.Member.User.ID
	if !d.AllowedUsers[user] {
		return nil, fmt.Errorf("Discord user is not allowlisted")
	}
	if len(d.AllowedGuilds) > 0 && !d.AllowedGuilds[interaction.GuildID] {
		return nil, fmt.Errorf("Discord guild is not allowlisted")
	}
	if len(d.AllowedChannels) > 0 && !d.AllowedChannels[interaction.ChannelID] {
		return nil, fmt.Errorf("Discord channel is not allowlisted")
	}
	question := strings.TrimPrefix(interaction.Data.CustomID, "answer:")
	if question == interaction.Data.CustomID || question == "" {
		return nil, fmt.Errorf("invalid Discord answer interaction")
	}
	userID := d.UserMap[user]
	if userID == "" {
		return nil, fmt.Errorf("Discord user is not mapped to a controller user")
	}
	if interaction.Type == 3 {
		modal := map[string]any{
			"type": 9,
			"data": map[string]any{
				"custom_id": interaction.Data.CustomID,
				"title":     "Answer vmbox",
				"components": []any{
					map[string]any{
						"type": 1,
						"components": []any{
							map[string]any{"type": 4, "custom_id": "answer", "label": "Answer", "style": 2, "required": true},
						},
					},
				},
			},
		}
		return modal, nil
	}
	if interaction.Type != 5 {
		return nil, fmt.Errorf("unsupported Discord interaction type")
	}
	answer := ""
	for _, row := range interaction.Data.Components {
		for _, item := range row.Components {
			if item.Value != "" {
				answer = item.Value
			}
		}
	}
	if answer == "" {
		return nil, fmt.Errorf("invalid Discord answer interaction")
	}
	if err := d.Answerer.Answer(ctx, d.AccountID, userID, question, answer); err != nil {
		return nil, err
	}
	return map[string]any{"type": 4, "data": map[string]any{"content": "Answer accepted", "flags": 64}}, nil
}

func send(client *http.Client, req *http.Request) error {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("notification HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}
