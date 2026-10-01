package controller

import (
	_ "embed"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
)

//go:embed activity_ranker.bin
var activityRankerBytes []byte

var activityRanker = func() activityphrase.Ranker {
	model, err := activityphrase.Decode(activityRankerBytes)
	if err != nil {
		panic(err)
	}
	return model
}()

// activityPhrase summarizes the latest agent action from bounded evidence.
func activityPhrase(text string) string {
	return activityRanker.Best(activityphrase.Candidates(text))
}
