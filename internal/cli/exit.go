package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

const defaultExitPromptTimeout = 30 * time.Second

type promptLine struct {
	text string
	err  error
}

func (a *App) postInteractiveExit(ctx context.Context, p provider.Provider, box provider.Box) error {
	reader := bufio.NewReader(a.In)
	timeout := a.ExitPromptTimeout
	if timeout <= 0 {
		timeout = defaultExitPromptTimeout
	}
	fmt.Fprintln(a.Err, "\nWhat should happen to this box?")
	fmt.Fprintln(a.Err, "  1. Keep running       (default)")
	fmt.Fprintln(a.Err, "  2. Hibernate          Save state, retain the volume, and free the compute slot")
	fmt.Fprintln(a.Err, "  3. Delete volume      Permanently delete this logical box and its workspace data")
	fmt.Fprint(a.Err, "Choice [1]: ")
	line, ok := readLineWithTimeout(ctx, reader, timeout)
	if !ok {
		return a.keepRunning(box.Name)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "1", "keep", "keep running":
		return a.keepRunning(box.Name)
	case "2", "hibernate":
		return a.hibernateBox(ctx, p, box)
	case "3", "delete", "delete volume":
		return a.confirmAndDeleteVolume(ctx, reader, timeout, p, box)
	default:
		fmt.Fprintln(a.Err, "vmbox: unrecognized choice; keeping the box running")
		return a.keepRunning(box.Name)
	}
}

func readLineWithTimeout(ctx context.Context, reader *bufio.Reader, timeout time.Duration) (string, bool) {
	result := make(chan promptLine, 1)
	go func() {
		line, err := reader.ReadString('\n')
		result <- promptLine{text: line, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", false
	case <-timer.C:
		return "", false
	case value := <-result:
		if value.err != nil && !errors.Is(value.err, io.EOF) {
			return "", false
		}
		if errors.Is(value.err, io.EOF) && value.text == "" {
			return "", false
		}
		return value.text, true
	}
}

func (a *App) keepRunning(name string) error {
	fmt.Fprintf(a.Err, "vmbox: keeping %q running; reconnect with: vmbox %s\n", name, name)
	return nil
}
func exactAttachedStorage(ctx context.Context, p provider.Provider, box provider.Box) (provider.Box, error) {
	if box.Storage != nil && box.Storage.ID != "" && box.Storage.Name != "" {
		return box, nil
	}
	inspector, ok := p.(provider.AttachedStorageProvider)
	if !ok {
		return box, nil
	}
	storage, err := inspector.AttachedStorage(ctx, box.ID)
	if err != nil {
		return box, err
	}
	box.Storage = storage
	return box, nil
}

func (a *App) hibernateBox(ctx context.Context, p provider.Provider, box provider.Box) error {
	fresh, err := p.Inspect(ctx, box.ID)
	if err != nil {
		return fmt.Errorf("inspect before hibernation (box remains running): %w", err)
	}
	fresh, err = exactAttachedStorage(ctx, p, fresh)
	if err != nil {
		return fmt.Errorf("inspect exact volume before hibernation (box remains running): %w", err)
	}
	if fresh.Storage == nil || fresh.Storage.ID == "" {
		return fmt.Errorf("hibernate %q: exact attached volume identity is unavailable; box remains running", fresh.Name)
	}
	detachable, ok := p.(provider.DetachableStorageProvider)
	if !ok {
		return fmt.Errorf("provider %s does not support safe volume detachment; box remains running", p.Name())
	}
	preparing := a.progress(ctx, fmt.Sprintf("saving tmux and workload state for %q", fresh.Name))
	result, execErr := p.Exec(ctx, fresh.ID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
	preparing()
	if execErr != nil {
		return fmt.Errorf("hibernate preparation failed; volume remains attached: %w", execErr)
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("remote command exited with status %d", result.ExitCode)
		}
		return fmt.Errorf("hibernate preparation failed; volume remains attached: %s", detail)
	}
	detaching := a.progress(ctx, fmt.Sprintf("detaching retained volume %s from %q", fresh.Storage.ID, fresh.Name))
	err = detachable.DetachStorage(ctx, fresh.ID, *fresh.Storage)
	detaching()
	if err != nil {
		return fmt.Errorf("volume was not safely detached; compute slot remains occupied: %w", err)
	}
	sanitizing := a.progress(ctx, fmt.Sprintf("starting a clean idle deployment for %q", fresh.Name))
	err = detachable.SanitizeSlot(ctx, fresh.ID)
	sanitizing()
	if err != nil {
		return fmt.Errorf("volume is retained and detached, but the compute slot needs recovery: %w", err)
	}
	fmt.Fprintf(a.Err, "vmbox: hibernated %q; retained volume %s (%s) and freed its compute slot\n", fresh.Name, fresh.Storage.Name, fresh.Storage.ID)
	return nil
}

func (a *App) confirmAndDeleteVolume(ctx context.Context, reader *bufio.Reader, timeout time.Duration, p provider.Provider, box provider.Box) error {
	fresh, err := p.Inspect(ctx, box.ID)
	if err != nil {
		return fmt.Errorf("inspect before volume deletion: %w", err)
	}
	fresh, err = exactAttachedStorage(ctx, p, fresh)
	if err != nil {
		return fmt.Errorf("inspect exact volume before deletion: %w", err)
	}
	if fresh.Storage == nil || fresh.Storage.ID == "" || fresh.Storage.Name == "" {
		return fmt.Errorf("delete volume %q: exact volume name and ID are unavailable; nothing was deleted", fresh.Name)
	}
	fmt.Fprintf(a.Err, "\nPERMANENT DELETION: logical box %q\nRailway volume: %s (ID: %s)\nAll workspace data will be permanently deleted.\nType %s to confirm: ", fresh.Name, fresh.Storage.Name, fresh.Storage.ID, fresh.Name)
	confirmation, ok := readLineWithTimeout(ctx, reader, timeout)
	if !ok || strings.TrimSpace(confirmation) != fresh.Name {
		fmt.Fprintln(a.Err, "vmbox: deletion cancelled; box and volume were kept")
		return nil
	}
	detachable, ok := p.(provider.DetachableStorageProvider)
	if !ok {
		return fmt.Errorf("provider %s does not support safe volume detachment; nothing was deleted", p.Name())
	}
	preparing := a.progress(ctx, fmt.Sprintf("stopping workload and flushing %q", fresh.Name))
	result, execErr := p.Exec(ctx, fresh.ID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
	preparing()
	if execErr != nil || result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if execErr != nil {
			detail = execErr.Error()
		}
		if detail == "" {
			detail = fmt.Sprintf("remote command exited with status %d", result.ExitCode)
		}
		return fmt.Errorf("volume deletion stopped safely before detachment: %s", detail)
	}
	detaching := a.progress(ctx, fmt.Sprintf("detaching volume %s", fresh.Storage.ID))
	err = detachable.DetachStorage(ctx, fresh.ID, *fresh.Storage)
	detaching()
	if err != nil {
		return fmt.Errorf("volume deletion stopped safely because detachment failed: %w", err)
	}
	deleting := a.progress(ctx, fmt.Sprintf("permanently deleting volume %s", fresh.Storage.ID))
	err = p.DeleteStorage(ctx, *fresh.Storage, fresh.Owner)
	deleting()
	if err != nil {
		return fmt.Errorf("delete confirmed volume: %w", err)
	}
	sanitizing := a.progress(ctx, fmt.Sprintf("sanitizing compute service %q", fresh.Name))
	err = detachable.SanitizeSlot(ctx, fresh.ID)
	sanitizing()
	if err != nil {
		return fmt.Errorf("volume was deleted, but the compute service needs recovery: %w", err)
	}
	fmt.Fprintf(a.Err, "vmbox: deleted only volume %s (%s); compute service %s remains in the fleet\n", fresh.Storage.Name, fresh.Storage.ID, fresh.ID)
	return nil
}

func (a *App) nonInteractiveFollowUp(name string) {
	fmt.Fprintf(a.Err, "vmbox: %q remains running. Hibernate: vmbox hibernate %s · Delete: vmbox delete-volume %s\n", name, name, name)
}
