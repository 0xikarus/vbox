package boxruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	imagepkg "image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ChatEvent struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	ReplyTo  string           `json:"replyTo"`
	Contact  string           `json:"contact,omitempty"`
	Text     string           `json:"text"`
	Images   []ChatEventImage `json:"images,omitempty"`
	Question *ChatQuestion    `json:"question,omitempty"`
}

type ChatEventImage struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type ChatQuestion struct {
	Text     string   `json:"text"`
	Choices  []string `json:"choices"`
	Multiple bool     `json:"multiple,omitempty"`
}

type ChatInbound struct {
	ID     string           `json:"id"`
	Text   string           `json:"text"`
	Images []ChatEventImage `json:"images,omitempty"`
}

type chatInboundFile struct {
	ID    string   `json:"id"`
	Text  string   `json:"text"`
	Paths []string `json:"paths,omitempty"`
}

var codexThreadNamingPause = waitForCodexThreadNaming

func waitForCodexThreadNaming(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func chatEventID() (string, error) {
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func chatSession(ctx context.Context) (string, error) {
	if value := os.Getenv("VMBOX_CHAT_SESSION"); value != "" {
		if err := validateTmuxToken("session", value); err != nil {
			return "", err
		}
		return value, nil
	}
	pane := os.Getenv("TMUX_PANE")
	if pane != "" {
		value, err := tmuxCommand(ctx, "", "display-message", "-p", "-t", pane, "#{session_name}")
		if err == nil {
			session := strings.TrimSpace(string(value))
			if validateTmuxToken("session", session) == nil {
				return session, nil
			}
		}
	}
	return chatSessionFromProcessTree(ctx, os.Getpid(), parentProcessID)
}

func chatSessionFromProcessTree(ctx context.Context, start int, parent func(int) (int, error)) (string, error) {
	value, err := tmuxCommand(ctx, "", "list-panes", "-a", "-F", "#{pane_pid}\t#{session_name}")
	if err != nil {
		return "", fmt.Errorf("managed chat session unavailable")
	}
	sessions := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(value)), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[0])
		session := strings.TrimSpace(fields[1])
		if parseErr == nil && validateTmuxToken("session", session) == nil {
			sessions[pid] = session
		}
	}
	pid := start
	for depth := 0; pid > 1 && depth < 128; depth++ {
		if session := sessions[pid]; session != "" {
			return session, nil
		}
		next, err := parent(pid)
		if err != nil || next <= 0 || next == pid {
			break
		}
		pid = next
	}
	return "", fmt.Errorf("managed chat session unavailable")
}

func parentProcessID(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			return strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
		}
	}
	return 0, fmt.Errorf("parent process unavailable")
}

func loadChatImages(paths []string) ([]ChatEventImage, error) {
	if len(paths) > 8 {
		return nil, fmt.Errorf("attach at most 8 images")
	}
	images := make([]ChatEventImage, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 8<<20 {
			return nil, fmt.Errorf("image file must be a regular file up to 8 MiB")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("could not read image file")
		}
		cfg, kind, err := imagepkg.DecodeConfig(bytes.NewReader(data))
		media := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[kind]
		if err != nil || media == "" || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return nil, fmt.Errorf("use PNG, JPEG, or GIF images up to 40 megapixels")
		}
		images = append(images, ChatEventImage{Name: filepath.Base(path), MediaType: media, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return images, nil
}

func writeChatEvent(ctx context.Context, event ChatEvent) error {
	session, err := chatSession(ctx)
	if err != nil {
		return err
	}
	if event.ID == "" {
		event.ID, err = chatEventID()
		if err != nil {
			return err
		}
	}
	directory := filepath.Join(os.Getenv("HOME"), ".local", "share", "vmbox", "chat", "outbox", session)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(directory, ".event-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(directory, event.ID+".json"))
}

func PullChatEvent(home, session string) (ChatEvent, bool, error) {
	var event ChatEvent
	if err := validateTmuxToken("session", session); err != nil {
		return event, false, err
	}
	directory := filepath.Join(home, ".local", "share", "vmbox", "chat", "outbox", session)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return event, false, nil
	}
	if err != nil {
		return event, false, err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				return event, false, err
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return event, false, fmt.Errorf("invalid chat event")
			}
			return event, true, nil
		}
	}
	return event, false, nil
}

func AckChatEvent(home, session, eventID string) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	if err := validateTmuxToken("event ID", eventID); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(home, ".local", "share", "vmbox", "chat", "outbox", session, eventID+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func StoreChatInbound(home, session string, inbound ChatInbound) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	if err := validateTmuxToken("message ID", inbound.ID); err != nil {
		return err
	}
	if strings.TrimSpace(inbound.Text) == "" || len(inbound.Text) > 140_000 || len(inbound.Images) > 8 {
		return fmt.Errorf("invalid inbound chat message")
	}
	directory := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session)
	filesDir := filepath.Join(directory, "files", inbound.ID)
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return err
	}
	event := chatInboundFile{ID: inbound.ID, Text: inbound.Text}
	for index, image := range inbound.Images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			return fmt.Errorf("invalid inbound image")
		}
		cfg, kind, err := imagepkg.DecodeConfig(bytes.NewReader(data))
		media := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[kind]
		if err != nil || media != image.MediaType || cfg.Width < 1 || cfg.Height < 1 || len(data) > 8<<20 {
			return fmt.Errorf("invalid inbound image")
		}
		extension := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}[media]
		path := filepath.Join(filesDir, fmt.Sprintf("image-%d%s", index+1, extension))
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
		event.Paths = append(event.Paths, path)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, inbound.ID+".json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, data, 0600)
}

func nextChatInbound(home, session string) (chatInboundFile, string, bool, error) {
	var event chatInboundFile
	directory := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return event, "", false, nil
	}
	if err != nil {
		return event, "", false, err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			path := filepath.Join(directory, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return event, "", false, err
			}
			if json.Unmarshal(data, &event) != nil {
				return event, "", false, fmt.Errorf("invalid inbound chat event")
			}
			return event, path, true, nil
		}
	}
	return event, "", false, nil
}

func DeliverCodexChat(ctx context.Context, root, home, session string, inbound ChatInbound) error {
	if err := StoreChatInbound(home, session, inbound); err != nil {
		return err
	}
	path := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, inbound.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var event chatInboundFile
	if json.Unmarshal(data, &event) != nil {
		return fmt.Errorf("invalid inbound chat event")
	}
	prompt := inbound.Text
	if len(event.Paths) > 0 {
		prompt += "\n\nAttached images are available as local files:\n"
		for index, image := range event.Paths {
			prompt += fmt.Sprintf("[Image %d]: %s\n", index+1, image)
		}
		prompt += "\nInspect the referenced images as message data before responding."
	}
	thread, err := CodexThreadID(root, home, session, WorkspaceDirectory())
	if err != nil {
		return err
	}
	args := []string{"queue", "--thread", thread, "--message", prompt}
	if err := runCodexQueue(ctx, home, path, args); err != nil {
		// A remembered id can outlive its thread; re-read once before failing.
		if forget := ForgetCodexThread(root, session); forget != nil {
			return err
		}
		thread, idErr := CodexThreadID(root, home, session, WorkspaceDirectory())
		if idErr != nil {
			return err
		}
		return runCodexQueue(ctx, home, path, []string{"queue", "--thread", thread, "--message", prompt})
	}
	return nil
}

// StartCodexChat starts a new interactive Codex session with the first Agent
// chat message in Codex's supported process arguments. Later messages use
// DeliverCodexChat after the thread has been named for the tmux session.
func StartCodexChat(ctx context.Context, root, home, session string, inbound ChatInbound) error {
	if err := StoreChatInbound(home, session, inbound); err != nil {
		return err
	}
	eventPath := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, inbound.ID+".json")
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return err
	}
	var event chatInboundFile
	if json.Unmarshal(data, &event) != nil {
		return fmt.Errorf("invalid inbound chat event")
	}
	argv, err := persistentAgentArgv(session, "codex")
	if err != nil {
		return err
	}
	for _, path := range event.Paths {
		argv = append(argv, "-i", path)
	}
	argv = append(argv, event.Text)
	// The initial message is already in argv. Removing its inbox envelope before
	// launch prevents any native message consumer from seeing it a second time.
	if err := os.Remove(eventPath); err != nil {
		return err
	}
	created, err := startTmuxTaskSession(ctx, root, session, "codex", argv)
	if err != nil {
		return err
	}
	if !created {
		// The session is already running Codex, so queue into it rather than
		// refusing and leaving the message undelivered.
		return DeliverCodexChat(ctx, root, home, session, inbound)
	}
	return nil
}

var runCodexQueue = func(ctx context.Context, home, eventPath string, args []string) error {
	command := exec.CommandContext(ctx, "codex", args...)
	command.Env = append(os.Environ(), "HOME="+home)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("codex queue failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return os.Remove(eventPath)
}

func NameCodexChatThread(ctx context.Context, root, session string) error {
	directory := filepath.Join(root, "chat", "codex-named")
	marker := filepath.Join(directory, session)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := waitForAgentReady(ctx, session, "codex"); err != nil {
		return err
	}
	if err := codexThreadNamingPause(ctx); err != nil {
		return err
	}
	if err := DeliverTmuxInput(ctx, root, session, "rename-v2-"+session, "/rename "+session, true); err != nil {
		return err
	}
	if err := agentReadySettlePause(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("named\n"), 0600)
}

func OpenCodeChatPort(session string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(session))
	return 20_000 + int(hash.Sum32()%10_000)
}

var openCodeReadyProbe = func(ctx context.Context, session string) (bool, error) {
	client, err := openCodeVisibleClient(ctx, os.Getenv("HOME"), session)
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vmbox-tui/health", nil)
	if err != nil {
		return false, err
	}
	response, err := client.Do(request)
	if err != nil {
		return false, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, nil
	}
	return true, nil
}

func DeliverOpenCodeChat(ctx context.Context, home, session string, inbound ChatInbound) error {
	if err := StoreChatInbound(home, session, inbound); err != nil {
		return err
	}
	eventPath := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, inbound.ID+".json")
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return err
	}
	var event chatInboundFile
	if json.Unmarshal(data, &event) != nil {
		return fmt.Errorf("invalid inbound chat event")
	}
	client, err := openCodeVisibleClient(ctx, home, session)
	if err != nil {
		return err
	}
	parts := []map[string]any{{"type": "text", "text": inbound.Text}}
	for _, path := range event.Paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		media, err := validateChatImage(data)
		if err != nil {
			return err
		}
		parts = append(parts, map[string]any{"type": "file", "mime": media, "filename": filepath.Base(path), "url": "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(data)})
	}
	payload, _ := json.Marshal(map[string]any{"parts": parts})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://vmbox-tui/prompt", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OpenCode prompt API returned status %d", response.StatusCode)
	}
	return os.Remove(eventPath)
}

func validateChatImage(data []byte) (string, error) {
	if len(data) < 1 || len(data) > 8<<20 {
		return "", fmt.Errorf("image must be up to 8 MiB")
	}
	config, kind, err := imagepkg.DecodeConfig(bytes.NewReader(data))
	media := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[kind]
	if err != nil || media == "" || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return "", fmt.Errorf("invalid image")
	}
	return media, nil
}
