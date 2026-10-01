package activityphrase

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
)

const generatorMagic = "ACTGEN01"

const (
	genPad = iota
	genBOS
	genEOS
	genUNK
	genSEP
	genRecent
)

var generatorToken = regexp.MustCompile(`#?[\pL\pN_]+(?:[._'-][\pL\pN_]+)*`)

type generatorTensor struct {
	shape []int
	data  []float32
}

// Generator runs the exported pointer-generator entirely in Go.
type Generator struct {
	embed, encHidden, decHidden int
	maxInput, maxOutput         int
	vocab                       []string
	wordID                      map[string]int
	tensors                     map[string]generatorTensor
}

func generatorHalf(value uint16) float32 {
	sign := uint32(value&0x8000) << 16
	exponent := int((value >> 10) & 0x1f)
	mantissa := uint32(value & 0x3ff)
	switch exponent {
	case 0:
		if mantissa == 0 {
			return math.Float32frombits(sign)
		}
		for mantissa&0x400 == 0 {
			mantissa <<= 1
			exponent--
		}
		mantissa &= 0x3ff
		exponent++
	case 31:
		return math.Float32frombits(sign | 0x7f800000 | mantissa<<13)
	}
	return math.Float32frombits(sign | uint32(exponent+112)<<23 | mantissa<<13)
}

type generatorReader struct {
	data []byte
	off  int
}

func (reader *generatorReader) bytes(count int) ([]byte, error) {
	if count < 0 || reader.off+count > len(reader.data) {
		return nil, errors.New("truncated activity generator")
	}
	value := reader.data[reader.off : reader.off+count]
	reader.off += count
	return value, nil
}

func (reader *generatorReader) u8() (int, error) {
	value, err := reader.bytes(1)
	if err != nil {
		return 0, err
	}
	return int(value[0]), nil
}

func (reader *generatorReader) u16() (int, error) {
	value, err := reader.bytes(2)
	if err != nil {
		return 0, err
	}
	return int(binary.LittleEndian.Uint16(value)), nil
}

func (reader *generatorReader) u32() (int, error) {
	value, err := reader.bytes(4)
	if err != nil {
		return 0, err
	}
	return int(binary.LittleEndian.Uint32(value)), nil
}

func LoadGenerator(data []byte) (*Generator, error) {
	reader := &generatorReader{data: data}
	magic, err := reader.bytes(8)
	if err != nil || string(magic) != generatorMagic {
		return nil, errors.New("invalid activity generator header")
	}
	model := &Generator{tensors: make(map[string]generatorTensor)}
	for _, field := range []*int{&model.embed, &model.encHidden, &model.decHidden, &model.maxInput, &model.maxOutput} {
		*field, err = reader.u16()
		if err != nil {
			return nil, err
		}
	}
	vocabSize, err := reader.u32()
	if err != nil {
		return nil, err
	}
	tensorCount, err := reader.u32()
	if err != nil {
		return nil, err
	}
	if model.embed < 1 || model.embed > 512 || model.encHidden < 1 || model.encHidden > 512 || model.decHidden < 1 || model.decHidden > 512 || model.maxInput < 1 || model.maxInput > 512 || model.maxOutput < 1 || model.maxOutput > 16 || vocabSize < 6 || vocabSize > 20000 || tensorCount > 64 {
		return nil, errors.New("invalid activity generator dimensions")
	}
	model.vocab = make([]string, vocabSize)
	model.wordID = make(map[string]int, vocabSize)
	for i := range model.vocab {
		length, err := reader.u16()
		if err != nil {
			return nil, err
		}
		word, err := reader.bytes(length)
		if err != nil {
			return nil, err
		}
		model.vocab[i] = string(word)
		model.wordID[model.vocab[i]] = i
	}
	if len(model.vocab) < 6 || model.vocab[genPad] != "<pad>" || model.vocab[genBOS] != "<bos>" || model.vocab[genEOS] != "<eos>" || model.vocab[genUNK] != "<unk>" || model.vocab[genSEP] != "<sep>" || model.vocab[genRecent] != "<recent>" {
		return nil, errors.New("invalid activity generator vocabulary")
	}
	for i := 0; i < tensorCount; i++ {
		nameSize, err := reader.u8()
		if err != nil {
			return nil, err
		}
		nameBytes, err := reader.bytes(nameSize)
		if err != nil {
			return nil, err
		}
		name := string(nameBytes)
		ndim, err := reader.u8()
		if err != nil || ndim < 1 || ndim > 3 {
			return nil, errors.New("invalid activity tensor rank")
		}
		tensor := generatorTensor{shape: make([]int, ndim)}
		count := 1
		for j := range tensor.shape {
			tensor.shape[j], err = reader.u32()
			if err != nil || tensor.shape[j] < 1 || tensor.shape[j] > 20000 || count > 20_000_000/tensor.shape[j] {
				return nil, errors.New("invalid activity tensor shape")
			}
			count *= tensor.shape[j]
		}
		raw, err := reader.bytes(count * 2)
		if err != nil {
			return nil, err
		}
		tensor.data = make([]float32, count)
		for j := range tensor.data {
			tensor.data[j] = generatorHalf(binary.LittleEndian.Uint16(raw[j*2:]))
		}
		model.tensors[name] = tensor
	}
	if reader.off != len(data) {
		return nil, errors.New("trailing activity generator bytes")
	}
	shapes := map[string][]int{
		"embedding.weight":             {vocabSize, model.embed},
		"encoder.weight_ih_l0":         {3 * model.encHidden, model.embed},
		"encoder.weight_hh_l0":         {3 * model.encHidden, model.encHidden},
		"encoder.bias_ih_l0":           {3 * model.encHidden},
		"encoder.bias_hh_l0":           {3 * model.encHidden},
		"encoder.weight_ih_l0_reverse": {3 * model.encHidden, model.embed},
		"encoder.weight_hh_l0_reverse": {3 * model.encHidden, model.encHidden},
		"encoder.bias_ih_l0_reverse":   {3 * model.encHidden},
		"encoder.bias_hh_l0_reverse":   {3 * model.encHidden},
		"init_proj.weight":             {model.decHidden, 2 * model.encHidden},
		"init_proj.bias":               {model.decHidden},
		"attn_enc.weight":              {model.decHidden, 2 * model.encHidden},
		"attn_dec.weight":              {model.decHidden, model.decHidden},
		"attn_v.weight":                {1, model.decHidden},
		"decoder.weight_ih":            {3 * model.decHidden, model.embed + 2*model.encHidden},
		"decoder.weight_hh":            {3 * model.decHidden, model.decHidden},
		"decoder.bias_ih":              {3 * model.decHidden},
		"decoder.bias_hh":              {3 * model.decHidden},
		"gen_proj.weight":              {model.embed, model.decHidden + 2*model.encHidden},
		"gen_proj.bias":                {model.embed},
		"gen_bias":                     {vocabSize},
		"p_gen.weight":                 {1, model.embed + 2*model.encHidden + model.decHidden},
		"p_gen.bias":                   {1},
	}
	for name, shape := range shapes {
		tensor, exists := model.tensors[name]
		if !exists || len(tensor.shape) != len(shape) {
			return nil, fmt.Errorf("missing or invalid activity tensor %s", name)
		}
		for j := range shape {
			if tensor.shape[j] != shape[j] {
				return nil, fmt.Errorf("invalid activity tensor %s", name)
			}
		}
	}
	return model, nil
}

func (model *Generator) tensor(name string) []float32 { return model.tensors[name].data }

func generatorDot(weights, vector []float32) float32 {
	if len(vector) == 0 {
		return 0
	}
	_ = weights[len(vector)-1]
	var a, b, c, d float32
	i := 0
	for ; i+3 < len(vector); i += 4 {
		a += weights[i] * vector[i]
		b += weights[i+1] * vector[i+1]
		c += weights[i+2] * vector[i+2]
		d += weights[i+3] * vector[i+3]
	}
	for ; i < len(vector); i++ {
		a += weights[i] * vector[i]
	}
	return (a + b) + (c + d)
}

func generatorLinear(weight, bias, input []float32, rows int) []float32 {
	cols := len(input)
	output := make([]float32, rows)
	for row := 0; row < rows; row++ {
		output[row] = generatorDot(weight[row*cols:(row+1)*cols], input)
		if bias != nil {
			output[row] += bias[row]
		}
	}
	return output
}

func generatorSigmoid(value float32) float32 { return 1 / (1 + float32(math.Exp(float64(-value)))) }

func generatorGRU(input, previous, wInput, wHidden, bInput, bHidden []float32) []float32 {
	hidden := len(previous)
	x := generatorLinear(wInput, bInput, input, 3*hidden)
	return generatorGRUProjected(x, previous, wHidden, bHidden)
}

func generatorGRUProjected(x, previous, wHidden, bHidden []float32) []float32 {
	hidden := len(previous)
	h := generatorLinear(wHidden, bHidden, previous, 3*hidden)
	next := make([]float32, hidden)
	for i := 0; i < hidden; i++ {
		reset := generatorSigmoid(x[i] + h[i])
		update := generatorSigmoid(x[hidden+i] + h[hidden+i])
		candidate := float32(math.Tanh(float64(x[2*hidden+i] + reset*h[2*hidden+i])))
		next[i] = (1-update)*candidate + update*previous[i]
	}
	return next
}

func (model *Generator) embedding(id int) []float32 {
	if id < 0 || id >= len(model.vocab) {
		id = genUNK
	}
	start := id * model.embed
	return model.tensor("embedding.weight")[start : start+model.embed]
}

func (model *Generator) evidenceTokens(text string) []string {
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && !strings.HasPrefix(line, "user: ") {
			lines = append(lines, line)
		}
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	var words []string
	for i, line := range lines {
		if len(words) > 0 {
			words = append(words, "<sep>")
		}
		if i >= len(lines)-2 {
			words = append(words, "<recent>")
		}
		words = append(words, generatorToken.FindAllString(strings.ToLower(line), -1)...)
	}
	if len(words) > model.maxInput {
		words = words[len(words)-model.maxInput:]
	}
	if len(words) == 0 {
		return []string{"<unk>"}
	}
	return words
}

func (model *Generator) encode(words []string) ([][]float32, []float32, [][]float32, []int, []string) {
	length := len(words)
	source, extended := make([]int, length), make([]int, length)
	unknowns := []string{}
	unknownID := make(map[string]int)
	for i, word := range words {
		id, known := model.wordID[word]
		if !known {
			id = genUNK
		}
		source[i] = id
		if id == genUNK && word != "<unk>" {
			if _, exists := unknownID[word]; !exists {
				unknownID[word] = len(model.vocab) + len(unknowns)
				unknowns = append(unknowns, word)
			}
			extended[i] = unknownID[word]
		} else {
			extended[i] = id
		}
	}
	forward, backward := make([][]float32, length), make([][]float32, length)
	forwardState, backwardState := make([]float32, model.encHidden), make([]float32, model.encHidden)
	forwardInputs, backwardInputs := make(map[int][]float32), make(map[int][]float32)
	forwardW, forwardB := model.tensor("encoder.weight_ih_l0"), model.tensor("encoder.bias_ih_l0")
	backwardW, backwardB := model.tensor("encoder.weight_ih_l0_reverse"), model.tensor("encoder.bias_ih_l0_reverse")
	for i, id := range source {
		input, ok := forwardInputs[id]
		if !ok {
			input = generatorLinear(forwardW, forwardB, model.embedding(id), 3*model.encHidden)
			forwardInputs[id] = input
		}
		forwardState = generatorGRUProjected(input, forwardState, model.tensor("encoder.weight_hh_l0"), model.tensor("encoder.bias_hh_l0"))
		forward[i] = forwardState
	}
	for i := length - 1; i >= 0; i-- {
		id := source[i]
		input, ok := backwardInputs[id]
		if !ok {
			input = generatorLinear(backwardW, backwardB, model.embedding(id), 3*model.encHidden)
			backwardInputs[id] = input
		}
		backwardState = generatorGRUProjected(input, backwardState, model.tensor("encoder.weight_hh_l0_reverse"), model.tensor("encoder.bias_hh_l0_reverse"))
		backward[i] = backwardState
	}
	encoded, projected := make([][]float32, length), make([][]float32, length)
	for i := range encoded {
		encoded[i] = append(append([]float32{}, forward[i]...), backward[i]...)
		projected[i] = generatorLinear(model.tensor("attn_enc.weight"), nil, encoded[i], model.decHidden)
	}
	initial := append(append([]float32{}, forwardState...), backwardState...)
	state := generatorLinear(model.tensor("init_proj.weight"), model.tensor("init_proj.bias"), initial, model.decHidden)
	for i := range state {
		state[i] = float32(math.Tanh(float64(state[i])))
	}
	return encoded, state, projected, extended, unknowns
}

func generatorSoftmax(logits []float32) []float32 {
	maximum := float32(math.Inf(-1))
	for _, value := range logits {
		if value > maximum {
			maximum = value
		}
	}
	var total float32
	for i := range logits {
		logits[i] = float32(math.Exp(float64(logits[i] - maximum)))
		total += logits[i]
	}
	for i := range logits {
		logits[i] /= total
	}
	return logits
}

func (model *Generator) step(previous int, state []float32, encoded, projected [][]float32, extended []int, unknownCount int) ([]float32, []float32) {
	embedded := model.embedding(previous)
	query := generatorLinear(model.tensor("attn_dec.weight"), nil, state, model.decHidden)
	attention := make([]float32, len(encoded))
	attentionWeight := model.tensor("attn_v.weight")
	for i := range attention {
		var energy float32
		for j := 0; j < model.decHidden; j++ {
			energy += attentionWeight[j] * float32(math.Tanh(float64(projected[i][j]+query[j])))
		}
		attention[i] = energy
	}
	attention = generatorSoftmax(attention)
	context := make([]float32, 2*model.encHidden)
	for i, weight := range attention {
		for j, value := range encoded[i] {
			context[j] += weight * value
		}
	}
	decoderInput := append(append([]float32{}, embedded...), context...)
	state = generatorGRU(decoderInput, state, model.tensor("decoder.weight_ih"), model.tensor("decoder.weight_hh"), model.tensor("decoder.bias_ih"), model.tensor("decoder.bias_hh"))
	outputInput := append(append([]float32{}, state...), context...)
	output := generatorLinear(model.tensor("gen_proj.weight"), model.tensor("gen_proj.bias"), outputInput, model.embed)
	for i := range output {
		output[i] = float32(math.Tanh(float64(output[i])))
	}
	logits := make([]float32, len(model.vocab))
	bias := model.tensor("gen_bias")
	for i := range logits {
		logits[i] = generatorDot(model.embedding(i), output) + bias[i]
	}
	vocab := generatorSoftmax(logits)
	pInput := append(append(append([]float32{}, embedded...), context...), state...)
	generation := generatorSigmoid(generatorDot(model.tensor("p_gen.weight"), pInput) + model.tensor("p_gen.bias")[0])
	probabilities := make([]float32, len(model.vocab)+unknownCount)
	for i, value := range vocab {
		probabilities[i] = generation * value
	}
	for i, id := range extended {
		probabilities[id] += (1 - generation) * attention[i]
	}
	return probabilities, state
}

// Generate greedily decodes at most eight tokens. The controller validates
// the display phrase before showing it.
func (model *Generator) Generate(text string) string {
	phrase, _ := model.GenerateScored(text)
	return phrase
}

// GenerateScored returns the mean log probability of emitted tokens as a
// confidence signal. The score excludes the end-of-sequence token.
func (model *Generator) GenerateScored(text string) (string, float64) {
	if model == nil {
		return "", math.Inf(-1)
	}
	encoded, state, projected, extended, unknowns := model.encode(model.evidenceTokens(text))
	previous := genBOS
	var output []string
	var logProbability float64
	for step := 0; step < model.maxOutput; step++ {
		probabilities, next := model.step(previous, state, encoded, projected, extended, len(unknowns))
		state = next
		for _, forbidden := range []int{genPad, genBOS, genUNK, genSEP, genRecent} {
			probabilities[forbidden] = -1
		}
		chosen := genEOS
		for i, value := range probabilities {
			if value > probabilities[chosen] {
				chosen = i
			}
		}
		if chosen == genEOS {
			break
		}
		logProbability += math.Log(math.Max(float64(probabilities[chosen]), 1e-12))
		if chosen < len(model.vocab) {
			output = append(output, model.vocab[chosen])
			previous = chosen
		} else {
			output = append(output, unknowns[chosen-len(model.vocab)])
			previous = genUNK
		}
	}
	phrase := strings.Join(output, " ")
	if phrase == "" {
		return "", math.Inf(-1)
	}
	runes := []rune(phrase)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes), logProbability / float64(len(output))
}
