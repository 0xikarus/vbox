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
	// Calibrate on the requested held-out 20%: among all score cutoffs, choose
	// the one with the widest coverage whose returned phrases are at least 80%
	// overlapping with the teacher phrase. Tool labels bypass this ranker.
	type scoredRow struct {
		score float32
		hit   bool
	}
	var scored []scoredRow
	var toolCount, toolHits int
	for _, row := range heldout {
		if label := activityphrase.LatestToolLabel(row.Text); label != "" {
			toolCount++
			if activityphrase.TokenOverlap(label, row.Phrase) >= .6 {
				toolHits++
			}
			continue
		}
		index, score := model.Top(row.Candidates)
		if index >= 0 {
			scored = append(scored, scoredRow{score, activityphrase.TokenOverlap(row.Candidates[index].Text, row.Phrase) >= .6})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	var hits, accepted, selectedHits int
	model.Threshold = 1e30 // A zero-coverage model is preferable to a false claim of precision.
	for i := 0; i < len(scored); {
		j := i
		for j < len(scored) && scored[j].score == scored[i].score {
			if scored[j].hit {
				hits++
			}
			j++
		}
		if hits*5 >= j*4 && j > accepted {
			accepted, selectedHits = j, hits
			model.Threshold = scored[i].score
		}
		i = j
	}
	fmt.Printf("labeled: %d, train: %d, validation: %d, held-out: %d\n", len(rows), len(training), len(validation), len(heldout))
	fmt.Printf("candidate recall at 0.6 overlap: %d/%d; span recall: %d/%d\n", phraseRecall, phraseCount, spanRecall, spanCount)
	fmt.Printf("held-out ranker precision@threshold: %d/%d; coverage: %d/%d; threshold: %.3f\n", selectedHits, accepted, accepted, len(heldout)-toolCount, model.Threshold)
	fmt.Printf("held-out direct tool labels: %d/%d overlap; total returned coverage: %d/%d\n", toolHits, toolCount, accepted+toolCount, len(heldout))
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
