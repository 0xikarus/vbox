package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"golang.org/x/text/width"
)

// Use the terminal's alternate screen and raw key events, not a line prompt.
// Read one byte at a time so selection keys cannot be buffered and replayed to
// SSH. All untrusted labels are escaped before reaching the terminal.
func (a *App) selectTUI(ctx context.Context, title string, labels []string, initial int) (int, error) {
	if len(labels) == 0 {
		return -1, fmt.Errorf("nothing to select")
	}
	if initial < 0 || initial >= len(labels) {
		initial = 0
	}
	restore, err := makeRaw(a.In)
	if err != nil {
		return -1, err
	}
	defer restore()
	fmt.Fprint(a.Err, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(a.Err, "\x1b[0m\x1b[?25h\x1b[?1049l")
	selected := initial
	lastW, lastH := 0, 0
	dirty := true
	for {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		width, height := 80, 24
		if f, ok := a.In.(*os.File); ok {
			if w, h, e := term.GetSize(int(f.Fd())); e == nil {
				width, height = w, h
			}
		}
		width = max(8, width)
		height = max(5, height)
		if dirty || width != lastW || height != lastH {
			page := max(1, height-5)
			start := max(0, min(selected-page/2, len(labels)-page))
			end := min(len(labels), start+page)
			var screen strings.Builder
			screen.WriteString("\x1b[H\x1b[2J\x1b[1m")
			screen.WriteString(tuiLabel(title, width-1))
			screen.WriteString("\x1b[0m\r\n")
			screen.WriteString(tuiLabel("↑/↓ select · Enter open · Esc/q cancel", width-1))
			screen.WriteString("\r\n\r\n")
			for i := start; i < end; i++ {
				if i == selected {
					screen.WriteString("\x1b[7m› ")
				} else {
					screen.WriteString("  ")
				}
				screen.WriteString(tuiLabel(labels[i], width-4))
				screen.WriteString("\x1b[0m\r\n")
			}
			fmt.Fprintf(&screen, "\x1b[%d;1H%d / %d", height, selected+1, len(labels))
			fmt.Fprint(a.Err, screen.String())
			lastW, lastH = width, height
			dirty = false
		}
		key, ready, err := a.selectionByte(ctx, 100)
		if err != nil {
			return -1, err
		}
		if !ready {
			continue
		}
		switch key {
		case '\r', '\n':
			return selected, nil
		case 3, 4, 'q':
			return -1, fmt.Errorf("selection cancelled")
		case 'k':
			selected = (selected + len(labels) - 1) % len(labels)
		case 'j', '\t':
			selected = (selected + 1) % len(labels)
		case 27:
			next, ok, err := a.selectionByte(ctx, 120)
			if err != nil {
				return -1, err
			}
			if !ok {
				return -1, fmt.Errorf("selection cancelled")
			}
			if next != '[' && next != 'O' {
				return -1, fmt.Errorf("selection cancelled")
			}
			next, ok, err = a.selectionByte(ctx, 120)
			if err != nil {
				return -1, err
			}
			if !ok {
				return -1, fmt.Errorf("incomplete key sequence")
			}
			switch next {
			case 'A':
				selected = (selected + len(labels) - 1) % len(labels)
			case 'B':
				selected = (selected + 1) % len(labels)
			case 'H':
				selected = 0
			case 'F':
				selected = len(labels) - 1
			}
		}
		dirty = true
	}
}

func tuiLabel(value string, columns int) string {
	// Quote controls/escape codes, then strip only the surrounding quotes.
	quoted := strconv.Quote(value)
	value = quoted[1 : len(quoted)-1]
	used := 0
	var out strings.Builder
	for _, r := range value {
		cells := 1
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			cells = 0
		} else if kind := width.LookupRune(r).Kind(); kind == width.EastAsianWide || kind == width.EastAsianFullwidth {
			cells = 2
		}
		if used+cells > columns-1 {
			out.WriteRune('…')
			return out.String()
		}
		out.WriteRune(r)
		used += cells
	}
	return out.String()
}

func (a *App) selectionByte(ctx context.Context, timeout int) (byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if f, ok := a.In.(*os.File); ok {
		poll := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, timeout)
		if err == unix.EINTR {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		if n == 0 {
			return 0, false, nil
		}
	}
	var b [1]byte
	_, err := io.ReadFull(a.In, b[:])
	if err != nil {
		return 0, false, err
	}
	return b[0], true, nil
}
