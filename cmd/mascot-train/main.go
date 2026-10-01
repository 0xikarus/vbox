// mascot-train regenerates the controller's bundled text-classification model.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/mascotclass"
)

func examples(path string) ([]mascotclass.Example, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result []mascotclass.Example
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		label, text, found := strings.Cut(line, "\t")
		if !found || text == "" {
			return nil, fmt.Errorf("invalid example on line %d of %s", len(result)+1, path)
		}
		result = append(result, mascotclass.Example{Label: label, Text: text})
	}
	return result, scanner.Err()
}

func main() {
	trainPath := flag.String("train", "scripts/mascot-data/train.tsv", "labeled training examples")
	evalPath := flag.String("eval", "scripts/mascot-data/eval.tsv", "validation examples")
	outPath := flag.String("out", "internal/controller/mascot_model.bin", "bundled model artifact")
	check := flag.Bool("check", false, "verify that the model artifact matches training data")
	flag.Parse()
	training, err := examples(*trainPath)
	if err != nil {
		panic(err)
	}
	model, err := mascotclass.Train(training, 80)
	if err != nil {
		panic(err)
	}
	evaluation, err := examples(*evalPath)
	if err != nil {
		panic(err)
	}
	correct := 0
	byLabel := make(map[string][2]int)
	for _, example := range evaluation {
		prediction, _ := model.Predict(example.Text)
		counts := byLabel[example.Label]
		counts[1]++
		if prediction == example.Label {
			counts[0]++
			correct++
		} else {
			fmt.Fprintf(os.Stderr, "miss: wanted %s, got %s: %s\n", example.Label, prediction, example.Text)
		}
		byLabel[example.Label] = counts
	}
	fmt.Printf("validation accuracy: %d/%d\n", correct, len(evaluation))
	for _, label := range mascotclass.Labels {
		counts := byLabel[label]
		fmt.Printf("%s: %d/%d\n", label, counts[0], counts[1])
	}
	artifact := model.Encode()
	if *check {
		current, err := os.ReadFile(*outPath)
		if err != nil || !bytes.Equal(current, artifact) {
			panic("bundled mascot model differs from training output")
		}
		return
	}
	if err := os.WriteFile(*outPath, artifact, 0644); err != nil {
		panic(err)
	}
	fmt.Printf("model: %d bytes\n", len(artifact))
}
