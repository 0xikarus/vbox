package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestBoxMessageThumbnailBoundsAndFormat(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 1000; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, source); err != nil {
		t.Fatal(err)
	}
	preview, err := boxMessageThumbnail(original.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || config.Width != 320 || config.Height != 160 {
		t.Fatalf("thumbnail format=%q dimensions=%dx%d", format, config.Width, config.Height)
	}
	if _, err := boxMessageThumbnail([]byte("invalid image")); err == nil {
		t.Fatal("invalid image unexpectedly produced a thumbnail")
	}
}

func TestDownloadBoxMessageImageThumbnailKeepsOriginal(t *testing.T) {
	store, mock := testStore(t)
	source := image.NewRGBA(image.Rect(0, 0, 640, 320))
	for y := 0; y < 320; y++ {
		for x := 0; x < 640; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, source); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		mock.ExpectQuery("SELECT i.media_type,i.data").
			WithArgs("account-a", "message-1", "image-1", "user-a", "owner").
			WillReturnRows(sqlmock.NewRows([]string{"media_type", "data"}).AddRow("image/png", original.Bytes()))
	}
	server := &Server{Store: store}
	principal := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	for _, thumbnail := range []bool{true, false} {
		path := "/v1/messages/message-1/images/image-1"
		if thumbnail {
			path += "?thumbnail=true"
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.SetPathValue("message", "message-1")
		request.SetPathValue("image", "image-1")
		response := httptest.NewRecorder()
		server.downloadBoxMessageImage(response, request, principal)
		if response.Code != http.StatusOK {
			t.Fatalf("thumbnail=%v status=%d body=%q", thumbnail, response.Code, response.Body.String())
		}
		if thumbnail {
			if response.Header().Get("Content-Type") != "image/jpeg" {
				t.Fatalf("thumbnail type=%q", response.Header().Get("Content-Type"))
			}
			config, _, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if config.Width != 320 || config.Height != 160 {
				t.Fatalf("thumbnail dimensions=%dx%d", config.Width, config.Height)
			}
		} else if response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), original.Bytes()) {
			t.Fatal("original download changed")
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

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

func TestBoxMessageReplyPointerInTextAndNativePrompts(t *testing.T) {
	longQuestion := "Which option should we take after reviewing the checks?  \n " + strings.Repeat("very long context ", 12)
	tests := []struct {
		name, body, parentBody, parentDirection, senderBoxID, want string
		native, missing                                            bool
	}{
		{"choice answer", "A, B", encodeBoxMessageQuestion(v1.BoxMessageQuestion{Text: longQuestion, Choices: []string{"A", "B"}}), "agent", "", "[Reply to parent-1: \"Agent: Which option should we take", false, false},
		{"normal reply in native prompt", "I agree", "Please inspect  \n the deployment", "user", "", "[Reply to parent-1: \"User: Please inspect the deployment\"]", true, false},
		{"missing parent", "I agree", "", "", "", "", false, true},
		{"contact reply", "Understood", "Can you review this?", "box", "other-box", "[Reply to parent-1: \"Box: Can you review this?\"]", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, mock := testStore(t)
			query := mock.ExpectQuery("SELECT .* FROM box_messages WHERE account_id=\\$1 AND task_id=\\$2 AND id::text=\\$3").
				WithArgs("account-a", "task-1", "parent-1")
			if tt.missing {
				query.WillReturnRows(emptyBoxMessageRows())
			} else {
				query.WillReturnRows(boxMessageRow("parent-1", "task-1", "user-a", tt.parentDirection, tt.parentBody, "delivered"))
			}
			if tt.senderBoxID != "" {
				mock.ExpectQuery("SELECT name FROM logical_boxes").WithArgs("account-a", tt.senderBoxID).
					WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Other box"))
			} else {
				now := time.Now().UTC()
				mock.ExpectQuery("SELECT created_at FROM box_messages").WithArgs("account-a", "message-1").
					WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
				mock.ExpectQuery("SELECT count\\(\\*\\) FROM box_messages").WithArgs("account-a", "task-1", now, "message-1").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			}
			mock.ExpectQuery("SELECT i.id::text,j.ordinal,i.download_token,i.media_type").WithArgs("account-a", "message-1").
				WillReturnRows(sqlmock.NewRows([]string{"id", "ordinal", "download_token", "media_type"}))
			server := &Server{Store: store}
			message := v1.BoxMessage{ID: "message-1", TaskID: "task-1", Text: tt.body, ParentMessageID: "parent-1", SenderBoxID: tt.senderBoxID}
			var prompt string
			var err error
			if tt.native {
				prompt, err = server.boxMessageNativePrompt(context.Background(), "account-a", "codex", message)
			} else {
				prompt, err = server.boxMessagePrompt(context.Background(), "account-a", "claude", message)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(prompt, tt.body) || strings.Count(prompt, tt.body) != 1 {
				t.Fatalf("reply body was changed or duplicated: %q", prompt)
			}
			if tt.missing {
				if strings.Contains(prompt, "[Reply to ") {
					t.Fatalf("missing parent still added a pointer: %q", prompt)
				}
			} else if !strings.Contains(prompt, tt.want) || strings.Index(prompt, "[Reply to ") > strings.Index(prompt, "[Message-ID:") {
				t.Fatalf("reply pointer absent or after instruction: %q", prompt)
			}
			if tt.name == "choice answer" {
				pointer := strings.Split(strings.Split(prompt, "[Reply to ")[1], "]")[0]
				if len([]rune(pointer)) > 150 || !strings.Contains(pointer, "…") {
					t.Fatalf("question context was not shortened: %q", prompt)
				}
			}
			if tt.senderBoxID != "" && !strings.Contains(prompt, "From-Box-ID: other-box") {
				t.Fatalf("contact instruction was lost: %q", prompt)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
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
