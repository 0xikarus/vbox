package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestBoxMessageNativePromptOmitsStructuredImageLinks(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT created_at FROM box_messages").
		WithArgs("account-a", "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM box_messages").
		WithArgs("account-a", "task-1", now, "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT i.id::text,j.ordinal,i.download_token,i.media_type").
		WithArgs("account-a", "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "ordinal", "download_token", "media_type"}).
			AddRow("image-1", 1, "image-token", "image/png").
			AddRow("video-1", 2, "video-token", "video/mp4"))

	server := &Server{Store: store, PublicURL: "https://controller.example"}
	message := v1.BoxMessage{ID: "message-1", TaskID: "task-1", Text: "inspect these"}
	prompt, err := server.boxMessageNativePrompt(context.Background(), "account-a", "codex", message)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "image-1") || strings.Contains(prompt, "image-token") {
		t.Fatalf("native image was also inserted as a link: %q", prompt)
	}
	if !strings.Contains(prompt, "video-1") || !strings.Contains(prompt, "video-token") {
		t.Fatalf("non-native video link was lost: %q", prompt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatInboundPayloadCarriesImageAsStructuredPartOnly(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT created_at FROM box_messages").
		WithArgs("account-a", "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM box_messages").
		WithArgs("account-a", "task-1", now, "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT i.id::text,j.ordinal,i.download_token,i.media_type").
		WithArgs("account-a", "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "ordinal", "download_token", "media_type"}).
			AddRow("image-1", 1, "image-token", "image/png").
			AddRow("video-1", 2, "video-token", "video/mp4"))
	imageBytes := []byte("png bytes")
	mock.ExpectQuery("SELECT i.media_type,i.data").
		WithArgs("account-a", "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"media_type", "data"}).AddRow("image/png", imageBytes))

	server := &Server{Store: store, PublicURL: "https://controller.example"}
	task := v1.BoxTask{ID: "task-1", Agent: "codex"}
	message := v1.BoxMessage{ID: "message-1", TaskID: "task-1", Text: "inspect these"}
	payload, err := server.chatInboundPayload(context.Background(), "account-a", task, message)
	if err != nil {
		t.Fatal(err)
	}
	var inbound boxruntime.ChatInbound
	if err := json.Unmarshal(payload, &inbound); err != nil {
		t.Fatal(err)
	}
	if len(inbound.Images) != 1 || inbound.Images[0].MediaType != "image/png" || inbound.Images[0].Data != base64.StdEncoding.EncodeToString(imageBytes) {
		t.Fatalf("structured images = %+v", inbound.Images)
	}
	if strings.Contains(inbound.Text, "image-1") || strings.Contains(inbound.Text, "image-token") {
		t.Fatalf("structured image was duplicated as a prompt link: %q", inbound.Text)
	}
	if !strings.Contains(inbound.Text, "video-1") {
		t.Fatalf("video fallback link was lost: %q", inbound.Text)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
