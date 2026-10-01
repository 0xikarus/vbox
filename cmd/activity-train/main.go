// activity-train extracts anonymized transcript snippets and trains the activity ranker.
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
	flag.Var(&codex, "codex", "Codex JSONL file or directory (repeatable)")
	flag.Var(&claude, "claude", "Claude JSONL file or directory (repeatable)")
	extract := flag.Bool("extract", false, "extract anonymized activity snippets")
	out := flag.String("out", "scripts/activity-data/snippets.jsonl", "output JSONL path")
	limit := flag.Int("limit", 2500, "maximum snippets to retain")
	flag.Parse()
	if !*extract {
		fmt.Fprintln(os.Stderr, "use -extract to build the unlabeled snippet corpus")
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
	count, err := extractSnippets(codex, claude, *out, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("extracted %d anonymized snippets to %s\n", count, *out)
}
