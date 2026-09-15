package controller

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const boxMessageQuestionPrefix = "vmbox-question:"

func encodeBoxMessageQuestion(question v1.BoxMessageQuestion) string {
	data, _ := json.Marshal(question)
	return boxMessageQuestionPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeBoxMessageQuestion(message *v1.BoxMessage) {
	if !strings.HasPrefix(message.Text, boxMessageQuestionPrefix) {
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(message.Text, boxMessageQuestionPrefix))
	if err != nil {
		return
	}
	var question v1.BoxMessageQuestion
	if json.Unmarshal(data, &question) != nil || strings.TrimSpace(question.Text) == "" || len(question.Choices) == 0 {
		return
	}
	message.Question = &question
	message.Text = question.Text
}
