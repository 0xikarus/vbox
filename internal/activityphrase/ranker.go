package activityphrase

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strings"
)

const featureCount = 1 << 14
const rankerMagic = "ACTRANK1"

// Example is one labeled transcript excerpt. Best is a candidate index or -1.
type Example struct {
	Candidates []Candidate
	Best       int
	Positives  []int
}

type Ranker struct {
	Weights   []float32
	Threshold float32
}

type feature struct {
	index int
	value float32
}

var rankerWords = regexp.MustCompile(`[\pL\pN]+`)

// TokenOverlap is the F1 overlap of unique, case-folded words in two phrases.
func TokenOverlap(left, right string) float64 {
	leftWords := rankerWords.FindAllString(strings.ToLower(left), -1)
	rightWords := rankerWords.FindAllString(strings.ToLower(right), -1)
	if len(leftWords) == 0 || len(rightWords) == 0 {
		return 0
	}
	leftSet, rightSet := make(map[string]bool), make(map[string]bool)
	for _, word := range leftWords {
		leftSet[word] = true
	}
	for _, word := range rightWords {
		rightSet[word] = true
	}
	common := 0
	for word := range leftSet {
		if rightSet[word] {
			common++
		}
	}
	return 2 * float64(common) / float64(len(leftSet)+len(rightSet))
}

func hashedFeature(name string) (int, float32) {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(name))
	value := hash.Sum64()
	sign := float32(1)
	if value&(1<<63) != 0 {
		sign = -1
	}
	return int(value & (featureCount - 1)), sign
}

func features(candidate Candidate) []feature {
	values := make(map[int]float32)
	add := func(name string, weight float32) {
		index, sign := hashedFeature(name)
		values[index] += sign * weight
	}
	add("bias", 1)
	phrase := strings.ToLower(candidate.Text)
	words := rankerWords.FindAllString(phrase, -1)
	for i, word := range words {
		add("word:"+word, 1)
		if i > 0 {
			add("bigram:"+words[i-1]+"_"+word, 1)
		}
	}
	runes := []rune("^" + phrase + "$")
	for length := 3; length <= 4; length++ {
		for i := 0; i+length <= len(runes); i++ {
			add("char:"+string(runes[i:i+length]), .25)
		}
	}
	if candidate.Tool {
		add("kind:tool", 1)
	} else {
		add("kind:agent", 1)
	}
	recency := candidate.Recency
	if recency > 8 {
		recency = 9
	}
	add("recency:"+string(rune('0'+recency)), 1)
	add("recency-linear", float32(1)/float32(1+candidate.Recency))
	position := candidate.Line
	if position > 11 {
		position = 11
	}
	add("line:"+string(rune('0'+position)), 1)
	add("line-linear", float32(position)/11)
	length := len([]rune(candidate.Text)) / 6
	if length > 5 {
		length = 5
	}
	add("length:"+string(rune('0'+length)), 1)
	wordCount := len(words)
	if wordCount > 5 {
		wordCount = 5
	}
	add("words:"+string(rune('0'+wordCount)), 1)
	result := make([]feature, 0, len(values))
	for index, value := range values {
		if value != 0 {
			result = append(result, feature{index, value})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].index < result[j].index })
	return result
}

func (model Ranker) score(feats []feature) float32 {
	var result float32
	for _, item := range feats {
		result += model.Weights[item.index] * item.value
	}
	return result
}

// Top returns the highest-scoring candidate and its raw score. It does not
// apply the abstention threshold, so training can calibrate that separately.
func (model Ranker) Top(candidates []Candidate) (int, float32) {
	if len(candidates) == 0 || len(model.Weights) != featureCount {
		return -1, 0
	}
	best, score := -1, float32(math.Inf(-1))
	for index, candidate := range candidates {
		current := model.score(features(candidate))
		if current > score {
			best, score = index, current
		}
	}
	return best, score
}

// Best returns a phrase only when the model ranks it above the learned
// abstention threshold.
func (model Ranker) Best(candidates []Candidate) string {
	index, score := model.Top(candidates)
	if index < 0 || score < model.Threshold {
		return ""
	}
	return candidates[index].Text
}

func Train(examples []Example, epochs int) Ranker {
	model := Ranker{Weights: make([]float32, featureCount)}
	type prepared struct {
		vectors  [][]feature
		best     int
		positive map[int]bool
	}
	var corpus []prepared
	for _, example := range examples {
		if len(example.Candidates) == 0 || example.Best < -1 || example.Best >= len(example.Candidates) {
			continue
		}
		row := prepared{best: example.Best, positive: make(map[int]bool)}
		for _, index := range example.Positives {
			if index >= 0 && index < len(example.Candidates) {
				row.positive[index] = true
			}
		}
		if len(row.positive) > 0 {
			row.best = -1
		}
		for _, candidate := range example.Candidates {
			row.vectors = append(row.vectors, features(candidate))
		}
		corpus = append(corpus, row)
	}
	random := rand.New(rand.NewSource(20261001))
	for epoch := 0; epoch < epochs; epoch++ {
		order := random.Perm(len(corpus))
		rate := float32(.035 / math.Sqrt(1+float64(epoch)*.3))
		for _, index := range order {
			row := corpus[index]
			scores := make([]float64, len(row.vectors))
			maximum := float64(0) // The abstention class has score zero.
			for i, vector := range row.vectors {
				scores[i] = float64(model.score(vector))
				if scores[i] > maximum {
					maximum = scores[i]
				}
			}
			denominator := math.Exp(-maximum)
			for _, score := range scores {
				denominator += math.Exp(score - maximum)
			}
			for i, vector := range row.vectors {
				probability := math.Exp(scores[i]-maximum) / denominator
				gradient := -float32(probability)
				if row.positive[i] {
					gradient += 1 / float32(len(row.positive))
				} else if i == row.best {
					gradient++
				}
				for _, item := range vector {
					model.Weights[item.index] += rate * (gradient*item.value - .0003*model.Weights[item.index])
				}
			}
		}
	}
	return model
}

// Calibrate chooses an abstention threshold using validation examples only.
func (model *Ranker) Calibrate(validation []Example) {
	if len(validation) == 0 {
		model.Threshold = 0
		return
	}
	type prediction struct {
		index, wanted int
		score         float32
	}
	var predictions []prediction
	var thresholds []float32
	for _, example := range validation {
		index, score := model.Top(example.Candidates)
		predictions = append(predictions, prediction{index, example.Best, score})
		thresholds = append(thresholds, score)
	}
	sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] < thresholds[j] })
	thresholds = append([]float32{thresholds[0] - 1}, thresholds...)
	thresholds = append(thresholds, thresholds[len(thresholds)-1]+1)
	bestCorrect := -1
	for _, threshold := range thresholds {
		correct := 0
		for _, item := range predictions {
			chosen := item.index
			if item.score < threshold {
				chosen = -1
			}
			if chosen == item.wanted {
				correct++
			}
		}
		if correct > bestCorrect {
			bestCorrect = correct
			model.Threshold = threshold
		}
	}
}

func (model Ranker) Encode() []byte {
	data := make([]byte, 8+4+featureCount*4)
	copy(data, rankerMagic)
	binary.LittleEndian.PutUint32(data[8:], math.Float32bits(model.Threshold))
	for index, weight := range model.Weights {
		binary.LittleEndian.PutUint32(data[12+index*4:], math.Float32bits(weight))
	}
	return data
}

func Decode(data []byte) (Ranker, error) {
	if len(data) != 12+featureCount*4 || string(data[:8]) != rankerMagic {
		return Ranker{}, errors.New("invalid activity ranker artifact")
	}
	model := Ranker{Weights: make([]float32, featureCount), Threshold: math.Float32frombits(binary.LittleEndian.Uint32(data[8:]))}
	for index := range model.Weights {
		model.Weights[index] = math.Float32frombits(binary.LittleEndian.Uint32(data[12+index*4:]))
	}
	return model, nil
}
