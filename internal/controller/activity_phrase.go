package controller

import (
	_ "embed"
	"strings"
	"unicode"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
)

//go:embed activity_model.bin
var activityModelBytes []byte

var activityGenerator = func() *activityphrase.Generator {
	model, err := activityphrase.LoadGenerator(activityModelBytes)
	if err != nil {
		panic(err)
	}
	return model
}()

// activityPhrase prefers factual harness labels, then generates a short phrase
// from bounded evidence. Invalid generations leave the UI's working fallback.
func activityPhrase(text string) string {
	if label := activityphrase.LatestToolLabel(text); label != "" {
		return label
	}
	phrase := activityGenerator.Generate(text)
	if validActivityPhrase(phrase) {
		return phrase
	}
	return ""
}

func validActivityPhrase(phrase string) bool {
	words := strings.Fields(phrase)
	if len(words) < 1 || len(words) > 6 || len([]rune(phrase)) > 32 {
		return false
	}
	runes := []rune(phrase)
	if !unicode.IsUpper(runes[0]) {
		return false
	}
	seen := make(map[string]bool, len(words))
	for _, word := range words {
		key := strings.ToLower(word)
		if seen[key] || strings.ContainsAny(word, "<>\n\r") {
			return false
		}
		seen[key] = true
	}
	return true
}
