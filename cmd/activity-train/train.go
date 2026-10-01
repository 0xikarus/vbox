package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
)

type teacherLabel struct {
	ID     string `json:"id"`
	Best   int    `json:"best"`
	Span   string `json:"span"`
	Phrase string `json:"phrase"`
}

type labeledSnippet struct {
	ID         string
	Text       string
	Candidates []activityphrase.Candidate
	Best       int
	Phrase     string
	Span       string
}

func readJSONLines(path string, consume func([]byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		if err := consume(scanner.Bytes()); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return scanner.Err()
}

func candidateMetadata(text string, phrases []string) []activityphrase.Candidate {
	found := make(map[string]activityphrase.Candidate)
	for _, candidate := range activityphrase.Candidates(text) {
		found[candidate.Text] = candidate
	}
	lines := strings.Split(text, "\n")
	result := make([]activityphrase.Candidate, 0, len(phrases))
	for _, phrase := range phrases {
		if candidate, ok := found[phrase]; ok {
			result = append(result, candidate)
			continue
		}
		candidate := activityphrase.Candidate{Text: phrase}
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.Contains(strings.ToLower(lines[i]), strings.ToLower(phrase)) {
				candidate.Line = i
				candidate.Recency = len(lines) - 1 - i
				candidate.Tool = strings.HasPrefix(lines[i], "tool: ")
				break
			}
		}
		result = append(result, candidate)
	}
	return result
}

func loadLabeledSnippets(paths inputPaths, labelPath string) ([]labeledSnippet, error) {
	samples := make(map[string]snippet)
	for _, path := range paths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := readJSONLines(path, func(line []byte) error {
			var sample snippet
			if err := json.Unmarshal(line, &sample); err != nil {
				return err
			}
			if sample.ID == "" || sample.Text == "" || len(sample.Candidates) == 0 {
				return fmt.Errorf("invalid snippet")
			}
			samples[sample.ID] = sample
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no snippets found")
	}
	labels := make(map[string]teacherLabel)
	if err := readJSONLines(labelPath, func(line []byte) error {
		var label teacherLabel
		if err := json.Unmarshal(line, &label); err != nil {
			return err
		}
		if label.ID == "" {
			return fmt.Errorf("label missing id")
		}
		labels[label.ID] = label
		return nil
	}); err != nil {
		return nil, err
	}
	var ids []string
	for id := range samples {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result []labeledSnippet
	for _, id := range ids {
		label, exists := labels[id]
		if !exists {
			return nil, fmt.Errorf("missing teacher label for %s", id)
		}
		sample := samples[id]
		if label.Best < -1 || label.Best >= len(sample.Candidates) {
			return nil, fmt.Errorf("invalid teacher index for %s", id)
		}
		phrases := append([]string(nil), sample.Candidates...)
		best := label.Best
		if best == -1 && label.Span != "" {
			positive := activityphrase.Normalize(label.Span)
			if positive == "" {
				return nil, fmt.Errorf("invalid positive span for %s", id)
			}
			phrases = append(phrases, positive)
			best = len(phrases) - 1
		}
		result = append(result, labeledSnippet{ID: id, Text: sample.Text, Candidates: candidateMetadata(sample.Text, phrases), Best: best, Phrase: label.Phrase, Span: label.Span})
	}
	return result, nil
}

func trainRanker(paths inputPaths, labels, out string, epochs int, check bool) error {
	rows, err := loadLabeledSnippets(paths, labels)
	if err != nil {
		return err
	}
	var training, validation, heldout []labeledSnippet
	var spanCount, spanRecall int
	for _, row := range rows {
		if row.Span != "" {
			spanCount++
			for _, candidate := range activityphrase.Candidates(row.Text) {
				if candidate.Text == activityphrase.Normalize(row.Span) || candidate.Text == row.Phrase {
					spanRecall++
					break
				}
			}
		}
		bucket := sha256.Sum256([]byte(row.ID))[0] % 5
		switch bucket {
		case 0:
			heldout = append(heldout, row)
		case 1:
			validation = append(validation, row)
		default:
			training = append(training, row)
		}
	}
	if len(training) == 0 || len(validation) == 0 || len(heldout) == 0 {
		return fmt.Errorf("need labeled snippets in training, validation, and held-out splits")
	}
	toExamples := func(rows []labeledSnippet) []activityphrase.Example {
		examples := make([]activityphrase.Example, 0, len(rows))
		for _, row := range rows {
			examples = append(examples, activityphrase.Example{Candidates: row.Candidates, Best: row.Best})
		}
		return examples
	}
	model := activityphrase.Train(toExamples(training), epochs)
	model.Calibrate(toExamples(validation))
	var indexCorrect, phraseCorrect int
	for _, row := range heldout {
		index, score := model.Top(row.Candidates)
		if index >= 0 && score < model.Threshold {
			index = -1
		}
		if index == row.Best {
			indexCorrect++
		}
		predicted := ""
		if index >= 0 {
			predicted = activityphrase.Normalize(row.Candidates[index].Text)
		}
		if predicted == row.Phrase {
			phraseCorrect++
		}
	}
	fmt.Printf("labeled: %d, train: %d, validation: %d, held-out: %d\n", len(rows), len(training), len(validation), len(heldout))
	fmt.Printf("span candidate recall: %d/%d\n", spanRecall, spanCount)
	fmt.Printf("held-out top-1 agreement: %d/%d; phrase match: %d/%d; threshold: %.3f\n", indexCorrect, len(heldout), phraseCorrect, len(heldout), model.Threshold)
	artifact := model.Encode()
	if check {
		current, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(current, artifact) {
			return fmt.Errorf("bundled activity ranker differs from training output")
		}
		if float64(phraseCorrect)/float64(len(heldout)) < .8 {
			return fmt.Errorf("held-out phrase match below 80%%")
		}
		return nil
	}
	if err := os.WriteFile(out, artifact, 0644); err != nil {
		return err
	}
	fmt.Printf("model: %d bytes\n", len(artifact))
	return nil
}
