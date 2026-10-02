// activity-train extracts activity snippets for private model training.
package main

import (
	"flag"
	"fmt"
	"os"
)

type inputPaths []string

func (paths *inputPaths) String() string { return fmt.Sprint([]string(*paths)) }
func (paths *inputPaths) Set(value string) error {
	*paths = append(*paths, value)
	return nil
}

func main() {
	var codex, claude inputPaths
	var redactions inputPaths
	flag.Var(&codex, "codex", "Codex JSONL file or directory (repeatable)")
	flag.Var(&claude, "claude", "Claude JSONL file or directory (repeatable)")
	flag.Var(&redactions, "redact", "additional account/profile name to remove (repeatable)")
	extract := flag.Bool("extract", false, "extract anonymized activity snippets")
	generator := flag.Bool("generator", false, "with -extract, sample agent prose tails for generator labels")
	out := flag.String("out", "", "output JSONL outside the repository")
	limit := flag.Int("limit", 2500, "maximum snippets to retain")
	flag.Parse()
	if !*extract {
		fmt.Fprintln(os.Stderr, "use -extract -out /private/activity-snippets.jsonl; model training is in scripts/activity-model/train.py")
		os.Exit(2)
	}
	if *limit < 1 {
		fmt.Fprintln(os.Stderr, "-limit must be positive")
		os.Exit(2)
	}
	if len(codex)+len(claude) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			panic(err)
		}
		codex = append(codex, home+"/.codex/sessions")
		claude = append(claude, home+"/.claude/projects")
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-extract requires -out outside the repository; keep your transcripts private")
		os.Exit(2)
	}
	var count int
	var err error
	if *generator {
		count, err = extractGeneratorSnippets(codex, claude, *out, *limit, redactions...)
	} else {
		count, err = extractSnippets(codex, claude, *out, *limit, redactions...)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("extracted %d anonymized snippets to %s\n", count, *out)
}
