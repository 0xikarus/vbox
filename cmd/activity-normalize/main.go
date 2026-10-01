// activity-normalize applies the harness's tool-label parser to synthetic
// examples before they enter the offline generator trainer.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

type example struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Phrase string `json:"phrase"`
}

func normalizeText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		command, ok := strings.CutPrefix(line, "tool: Running ")
		if !ok || command == "command" || command == "node tests" {
			continue
		}
		input, _ := json.Marshal(map[string]string{"command": command})
		lines[i] = "tool: " + boxruntime.MascotToolLabel("Bash", input)
	}
	return strings.Join(lines, "\n")
}

func run(input, output string) error {
	source, err := os.Open(input)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.Create(output)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(destination)
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var item example
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			destination.Close()
			return err
		}
		if item.ID == "" || item.Phrase == "" {
			destination.Close()
			return fmt.Errorf("synthetic example missing id or phrase")
		}
		item.Text = normalizeText(item.Text)
		data, err := json.Marshal(item)
		if err != nil {
			destination.Close()
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			destination.Close()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		destination.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		destination.Close()
		return err
	}
	return destination.Close()
}

func main() {
	input := flag.String("input", "scripts/activity-data/synthetic.jsonl", "synthetic training JSONL")
	output := flag.String("output", "", "normalized JSONL path")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "-output is required")
		os.Exit(2)
	}
	if err := run(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
