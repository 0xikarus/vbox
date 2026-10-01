// Package mascotclass implements a small supervised text classifier for
// transcript status. Training and inference use the same feature extraction.
package mascotclass

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"unicode"
)

const (
	Buckets = 1 << 14
	classes = 6
)

var Labels = [classes]string{"idle", "working", "waiting", "angry", "happy", "laughing"}

type Example struct {
	Label string
	Text  string
}

type Feature struct {
	Index uint32
	Value float32
}

type Model struct {
	weights []float32
	bias    [classes]float32
}

func NewModel() *Model {
	return &Model{weights: make([]float32, classes*Buckets)}
}

func labelIndex(label string) int {
	for i, candidate := range Labels {
		if label == candidate {
			return i
		}
	}
	return -1
}

func hashFeature(value string) uint32 {
	const offset, prime uint32 = 2166136261, 16777619
	hash := offset
	for i := 0; i < len(value); i++ {
		hash = (hash ^ uint32(value[i])) * prime
	}
	return hash & (Buckets - 1)
}

func words(text string) []string {
	var result []string
	var token []rune
	for _, char := range strings.ToLower(text) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) || unicode.IsSymbol(char) {
			token = append(token, char)
			continue
		}
		if len(token) > 0 {
			result = append(result, string(token))
			token = token[:0]
		}
	}
	if len(token) > 0 {
		result = append(result, string(token))
	}
	return result
}

// Features combine word and character n-grams. Character features let a
// learned weight transfer across related word forms and spelling variants.
func Features(text string) []Feature {
	features := make(map[uint32]float32, 128)
	terms := words(text)
	for i, term := range terms {
		features[hashFeature("w:"+term)]++
		if i > 0 {
			features[hashFeature("b:"+terms[i-1]+" "+term)]++
		}
		chars := []rune("<" + term + ">")
		for size := 3; size <= 5; size++ {
			for start := 0; start+size <= len(chars); start++ {
				features[hashFeature("c:"+string(chars[start:start+size]))]++
			}
		}
	}
	var norm float64
	for _, count := range features {
		norm += float64(count * count)
	}
	scale := float32(0)
	if norm > 0 {
		scale = float32(1 / math.Sqrt(norm))
	}
	result := make([]Feature, 0, len(features))
	for key, count := range features {
		result = append(result, Feature{key, count * scale})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

func (m *Model) scores(features []Feature) [classes]float32 {
	result := m.bias
	for _, feature := range features {
		for class := range Labels {
			result[class] += m.weights[class*Buckets+int(feature.Index)] * feature.Value
		}
	}
	return result
}

func probabilities(scores [classes]float32) [classes]float32 {
	maxScore := scores[0]
	for _, score := range scores[1:] {
		if score > maxScore {
			maxScore = score
		}
	}
	var sum float64
	var result [classes]float32
	for i, score := range scores {
		result[i] = float32(math.Exp(float64(score - maxScore)))
		sum += float64(result[i])
	}
	for i := range result {
		result[i] /= float32(sum)
	}
	return result
}

func (m *Model) Predict(text string) (string, [classes]float32) {
	result := probabilities(m.scores(Features(text)))
	best := 0
	for i := 1; i < classes; i++ {
		if result[i] > result[best] {
			best = i
		}
	}
	return Labels[best], result
}

// Classify accepts arbitrary prose. It considers the newest sentences in a
// bounded excerpt so a recent result can supersede earlier context.
func (m *Model) Classify(text string) string {
	const maxBytes = 8192
	if len(text) > maxBytes {
		text = text[len(text)-maxBytes:]
		for len(text) > 0 && text[0]&0xc0 == 0x80 {
			text = text[1:]
		}
	}
	segments := strings.FieldsFunc(text, func(char rune) bool {
		return char == '\n' || char == '.' || char == '?' || char == '!' || char == ';'
	})
	var votes [classes]float64
	seen, weight := 0, 1.0
	for i := len(segments) - 1; i >= 0 && seen < 8; i-- {
		segment := strings.TrimSpace(segments[i])
		if segment == "" {
			continue
		}
		_, probability := m.Predict(segment)
		for class, value := range probability {
			votes[class] += float64(value) * weight
		}
		seen++
		weight *= 0.5
	}
	if seen == 0 {
		return "idle"
	}
	best := 0
	for class := 1; class < classes; class++ {
		if votes[class] > votes[best] {
			best = class
		}
	}
	return Labels[best]
}

// Train fits a multiclass logistic regression model using deterministic SGD.
// Data is fixed at build time; the controller only loads the resulting weights.
func Train(examples []Example, epochs int) (*Model, error) {
	if len(examples) == 0 || epochs < 1 {
		return nil, fmt.Errorf("training data and epochs required")
	}
	type row struct {
		label    int
		features []Feature
	}
	rows := make([]row, len(examples))
	for i, example := range examples {
		label := labelIndex(example.Label)
		if label < 0 || strings.TrimSpace(example.Text) == "" {
			return nil, fmt.Errorf("invalid training example %d", i)
		}
		rows[i] = row{label, Features(example.Text)}
	}
	model := NewModel()
	rng := rand.New(rand.NewSource(1))
	for epoch := 0; epoch < epochs; epoch++ {
		order := rng.Perm(len(rows))
		learningRate := float32(0.45 / (1 + float64(epoch)*0.025))
		for _, index := range order {
			row := rows[index]
			probability := probabilities(model.scores(row.features))
			for class := range Labels {
				gradient := -probability[class]
				if class == row.label {
					gradient++
				}
				model.bias[class] += learningRate * gradient
				base := class * Buckets
				for _, feature := range row.features {
					offset := base + int(feature.Index)
					model.weights[offset] = model.weights[offset]*0.99999 + learningRate*gradient*feature.Value
				}
			}
		}
	}
	return model, nil
}

const magic = "VMMOOD1\x00"

func (m *Model) Encode() []byte {
	result := make([]byte, len(magic)+4+4*classes+4*len(m.weights))
	copy(result, magic)
	binary.LittleEndian.PutUint32(result[len(magic):], Buckets)
	offset := len(magic) + 4
	for _, value := range m.bias {
		binary.LittleEndian.PutUint32(result[offset:], math.Float32bits(value))
		offset += 4
	}
	for _, value := range m.weights {
		binary.LittleEndian.PutUint32(result[offset:], math.Float32bits(value))
		offset += 4
	}
	return result
}

func Load(data []byte) (*Model, error) {
	expected := len(magic) + 4 + 4*classes + 4*classes*Buckets
	if len(data) != expected || string(data[:len(magic)]) != magic || binary.LittleEndian.Uint32(data[len(magic):]) != Buckets {
		return nil, fmt.Errorf("invalid mascot model")
	}
	model := NewModel()
	offset := len(magic) + 4
	for i := range model.bias {
		model.bias[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
	}
	for i := range model.weights {
		model.weights[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
	}
	return model, nil
}
