package controller

import "testing"

func TestActivityPhrasePrefersNewestToolLabel(t *testing.T) {
	text := "assistant: I am inspecting the build.\ntool: Running go test"
	if got := activityPhrase(text); got != "Running go test" {
		t.Fatalf("latest tool label = %q", got)
	}
}

func TestActivityPhraseRejectsInvalidGenerations(t *testing.T) {
	for _, phrase := range []string{
		"fixing the build",                      // Must start with a capital.
		"Fixing fixing the build",               // Repeated token.
		"Checking one two three four five six",  // More than six words.
		"Editing <unk>",                         // Decoder control token.
		"Reviewing extraordinarilylongfilename", // More than 32 characters.
	} {
		if validActivityPhrase(phrase) {
			t.Errorf("accepted invalid phrase %q", phrase)
		}
	}
	if !validActivityPhrase("Fixing the mobile layout") {
		t.Fatal("rejected normal activity phrase")
	}
}
