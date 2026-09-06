package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

type formField struct {
	Label   string
	Value   string
	Choices []string
	Hidden  bool
	When    func() bool
	Secret  bool
}

// A single alternate-screen lifetime covers edits, inline choices, submission,
// progress and recoverable errors. Submit is the only mutation boundary.
func (a *App) runForm(ctx context.Context, title string, fields []*formField, submit func(func(string)) error) error {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return fmt.Errorf("dialog requires a terminal; use explicit CLI arguments")
	}
	restore, err := makeRaw(a.In)
	if err != nil {
		return err
	}
	defer restore()
	fmt.Fprint(a.Err, "\x1b[?1049h\x1b[?25l\x1b[?2004h")
	defer fmt.Fprint(a.Err, "\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
	selected, editing := 0, false
	status := ""
	visible := func() []*formField {
		out := []*formField{}
		for _, f := range fields {
			if !f.Hidden && (f.When == nil || f.When()) {
				out = append(out, f)
			}
		}
		return out
	}
	width, height := 80, 24
	lastScreen := ""
	render := func() {
		if f, ok := a.In.(*os.File); ok {
			if w, h, e := term.GetSize(int(f.Fd())); e == nil {
				width, height = w, h
			}
		}
		width, height = max(12, width), max(6, height)
		rows := visible()
		selected = min(selected, len(rows))
		lines := make([]string, 0, len(rows)+1)
		for i, f := range rows {
			value := f.Value
			if f.Secret && value != "" {
				value = "••••••"
			}
			line := fmt.Sprintf("%-18s %s", f.Label, value)
			if len(f.Choices) > 0 {
				line += "  ‹ ›"
			}
			if editing && i == selected {
				line += " ▏"
			}
			lines = append(lines, line)
		}
		lines = append(lines, "[ Create ]    Esc: cancel")
		statusLines := formStatusLines(status, width-1)
		statusLines = statusLines[:min(len(statusLines), max(1, height-6))]
		page := max(1, height-5-len(statusLines))
		start := max(0, min(selected-page/2, len(lines)-page))
		var b strings.Builder
		fmt.Fprintf(&b, "\x1b[H\x1b[2J%s\r\n%s\r\n\r\n", tuiLabel(title, width-1), tuiLabel("↑/↓ Tab: move · ←/→: choose · Enter: edit/create", width-1))
		for _, line := range statusLines {
			fmt.Fprintf(&b, "%s\r\n", line)
		}
		for i := start; i < min(len(lines), start+page); i++ {
			if i == selected {
				b.WriteString("\x1b[7m› ")
			} else {
				b.WriteString("  ")
			}
			b.WriteString(tuiLabel(lines[i], width-4))
			b.WriteString("\x1b[0m\r\n")
		}
		if screen := b.String(); screen != lastScreen {
			fmt.Fprint(a.Err, screen)
			lastScreen = screen
		}
	}
	cycle := func(delta int) {
		rows := visible()
		if selected >= len(rows) {
			return
		}
		f := rows[selected]
		if len(f.Choices) == 0 {
			return
		}
		i := 0
		for n, v := range f.Choices {
			if v == f.Value {
				i = n
				break
			}
		}
		f.Value = f.Choices[(i+delta+len(f.Choices))%len(f.Choices)]
	}
	for {
		render()
		key, ready, err := a.selectionByte(ctx, 100)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		rows := visible()
		if key == 3 || key == 4 {
			return fmt.Errorf("creation cancelled")
		}
		if key == 27 {
			next, ok, err := a.selectionByte(ctx, 120)
			if err != nil {
				return err
			}
			if !ok {
				if editing {
					editing = false
					continue
				}
				return fmt.Errorf("creation cancelled")
			}
			if next != '[' && next != 'O' {
				continue
			}
			code, ok, err := a.selectionByte(ctx, 120)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if code == '2' { // bracketed paste: never interpret pasted Enter as Create
				var sequence strings.Builder
				for sequence.Len() < 4 {
					v, ok, e := a.selectionByte(ctx, 120)
					if e != nil {
						return e
					}
					if !ok {
						break
					}
					sequence.WriteByte(v)
					if v == '~' {
						break
					}
				}
				if sequence.String() == "00~" {
					var paste strings.Builder
					for {
						v, ok, e := a.selectionByte(ctx, 1000)
						if e != nil {
							return e
						}
						if !ok {
							return fmt.Errorf("paste interrupted")
						}
						paste.WriteByte(v)
						if strings.HasSuffix(paste.String(), "\x1b[201~") {
							break
						}
						if paste.Len() > 65536 {
							return fmt.Errorf("paste exceeds 64 KiB")
						}
					}
					if editing && selected < len(rows) {
						text := strings.TrimSuffix(paste.String(), "\x1b[201~")
						text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
						rows[selected].Value += text
					}
				}
				continue
			}
			switch code {
			case 'A':
				editing = false
				selected = (selected + len(rows)) % (len(rows) + 1)
			case 'B':
				editing = false
				selected = (selected + 1) % (len(rows) + 1)
			case 'C':
				if !editing {
					cycle(1)
				}
			case 'D':
				if !editing {
					cycle(-1)
				}
			case 'Z':
				editing = false
				selected = (selected + len(rows)) % (len(rows) + 1)
			}
			continue
		}
		if key == '\t' {
			editing = false
			selected = (selected + 1) % (len(rows) + 1)
			continue
		}
		if key == '\r' || key == '\n' {
			if editing {
				editing = false
				continue
			}
			if selected == len(rows) {
				status = "Creating…"
				render()
				if err := submit(func(message string) { status = message; render() }); err != nil {
					status = err.Error()
					continue
				}
				return nil
			}
			if len(rows[selected].Choices) > 0 {
				cycle(1)
			} else {
				editing = true
			}
			continue
		}
		if !editing || selected >= len(rows) {
			continue
		}
		f := rows[selected]
		if key == 127 || key == 8 {
			if len(f.Value) > 0 {
				_, n := utf8.DecodeLastRuneInString(f.Value)
				f.Value = f.Value[:len(f.Value)-n]
			}
			continue
		}
		if key == 21 {
			f.Value = ""
			continue
		}
		if key < 32 {
			continue
		}
		data := []byte{key}
		for !utf8.FullRune(data) {
			v, ok, e := a.selectionByte(ctx, 120)
			if e != nil {
				return e
			}
			if !ok {
				break
			}
			data = append(data, v)
		}
		if utf8.Valid(data) && len(f.Value)+len(data) <= 16384 {
			f.Value += string(data)
		}
	}
}

// Keep actionable errors beside the form instead of clipping them to a footer.
func formStatusLines(status string, width int) []string {
	if status == "" {
		return nil
	}
	width = max(1, width)
	var lines []string
	for _, paragraph := range strings.Split(status, "\n") {
		runes := []rune(tuiLabel(paragraph, len([]rune(paragraph))+1))
		for len(runes) > width {
			end := width
			for i := width; i > 0; i-- {
				if runes[i] == ' ' {
					end = i
					break
				}
			}
			lines = append(lines, string(runes[:end]))
			runes = []rune(strings.TrimLeft(string(runes[end:]), " "))
		}
		lines = append(lines, string(runes))
	}
	return lines
}
