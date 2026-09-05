package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"time"
)

const defaultExitPromptTimeout = 30 * time.Second

type promptLine struct {
	text string
	err  error
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
