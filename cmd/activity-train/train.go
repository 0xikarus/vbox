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
		result = append(result, labeledSnippet{ID: id, Text: sample.Text, Candidates: activityphrase.Candidates(sample.Text), Best: label.Best, Phrase: label.Phrase, Span: label.Span})
	}
	return result, nil
}

func trainRanker(paths inputPaths, labels, out string, epochs int, check bool) error {
	rows, err := loadLabeledSnippets(paths, labels)
	if err != nil {
		return err
	}
	var training, validation, heldout []labeledSnippet
	var spanCount, spanRecall, phraseCount, phraseRecall int
	for _, row := range rows {
		if row.Span != "" {
			spanCount++
		}
		if row.Best >= 0 || row.Span != "" {
			phraseCount++
		}
		found := false
		for _, candidate := range row.Candidates {
			if activityphrase.TokenOverlap(candidate.Text, row.Phrase) >= .6 {
				found = true
				break
			}
		}
		if found {
			if row.Span != "" {
				spanRecall++
			}
			if row.Best >= 0 || row.Span != "" {
				phraseRecall++
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
			candidates := append([]activityphrase.Candidate(nil), row.Candidates...)
			var positives []int
			if row.Best >= 0 || row.Span != "" {
				for index, candidate := range candidates {
					if activityphrase.TokenOverlap(candidate.Text, row.Phrase) >= .5 {
						positives = append(positives, index)
					}
				}
			}
			if row.Span != "" {
				phrase := activityphrase.Normalize(row.Span)
				if phrase != "" {
					index := -1
					for i, candidate := range candidates {
						if candidate.Text == phrase {
							index = i
							break
						}
					}
					if index == -1 {
						candidates = append(candidates, candidateMetadata(row.Text, []string{phrase})[0])
						index = len(candidates) - 1
					}
					positives = append(positives, index)
				}
			}
			examples = append(examples, activityphrase.Example{Candidates: candidates, Best: -1, Positives: positives})
		}
		return examples
	}
	model := activityphrase.Train(toExamples(training), epochs)
	type scoredRow struct {
		row   labeledSnippet
		index int
		score float32
	}
	var scoredValidation []scoredRow
	var thresholds []float32
	for _, row := range validation {
		index, score := model.Top(row.Candidates)
		scoredValidation = append(scoredValidation, scoredRow{row, index, score})
		thresholds = append(thresholds, score)
	}
	sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] < thresholds[j] })
	thresholds = append([]float32{thresholds[0] - 1}, thresholds...)
	thresholds = append(thresholds, thresholds[len(thresholds)-1]+1)
	bestValidation := -1
	for _, threshold := range thresholds {
		correct := 0
		for _, scored := range scoredValidation {
			if phraseHitPrediction(scored.row, scored.index, scored.score, threshold) {
				correct++
			}
		}
		if correct > bestValidation {
			bestValidation = correct
			model.Threshold = threshold
		}
	}
	var topOneHits, exactMatches int
	for _, row := range heldout {
		if phraseHit(model, row, model.Threshold) {
			topOneHits++
		}
		if model.Best(row.Candidates) == row.Phrase {
			exactMatches++
		}
	}
	fmt.Printf("labeled: %d, train: %d, validation: %d, held-out: %d\n", len(rows), len(training), len(validation), len(heldout))
	fmt.Printf("candidate recall at 0.6 overlap: %d/%d; span recall: %d/%d\n", phraseRecall, phraseCount, spanRecall, spanCount)
	fmt.Printf("held-out top-1 overlap hit: %d/%d; exact phrase: %d/%d; threshold: %.3f\n", topOneHits, len(heldout), exactMatches, len(heldout), model.Threshold)
	artifact := model.Encode()
	if check {
		current, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(current, artifact) {
			return fmt.Errorf("bundled activity ranker differs from training output")
		}
		return nil
	}
	if err := os.WriteFile(out, artifact, 0644); err != nil {
		return err
	}
	fmt.Printf("model: %d bytes\n", len(artifact))
	return nil
}

func phraseHit(model activityphrase.Ranker, row labeledSnippet, threshold float32) bool {
	index, score := model.Top(row.Candidates)
	return phraseHitPrediction(row, index, score, threshold)
}

func phraseHitPrediction(row labeledSnippet, index int, score, threshold float32) bool {
	if index < 0 || score < threshold {
		return row.Best == -1 && row.Span == ""
	}
	return activityphrase.TokenOverlap(activityphrase.Normalize(row.Candidates[index].Text), row.Phrase) >= .6
}
