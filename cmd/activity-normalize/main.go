// activity-normalize applies the harness's tool-label parser and shared
// evidence normalization to real and synthetic generator examples.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
)

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
		var item map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			destination.Close()
			return err
		}
		var id, content string
		if err := json.Unmarshal(item["id"], &id); err != nil || id == "" {
			destination.Close()
			return fmt.Errorf("activity example missing id")
		}
		if err := json.Unmarshal(item["text"], &content); err != nil {
			destination.Close()
			return fmt.Errorf("activity example %s missing text: %w", id, err)
		}
		item["text"], _ = json.Marshal(activityphrase.NormalizeEvidence(content))
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
	input := flag.String("input", "scripts/activity-data/synthetic.jsonl", "activity example JSONL")
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
