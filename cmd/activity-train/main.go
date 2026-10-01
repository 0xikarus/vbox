// activity-train extracts anonymized transcript snippets and trains the activity ranker.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
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
	check := flag.Bool("check", false, "verify the bundled ranker and report held-out accuracy")
	out := flag.String("out", "", "output JSONL or ranker path")
	limit := flag.Int("limit", 2500, "maximum snippets to retain")
	labels := flag.String("labels", "scripts/activity-data/labels.jsonl", "teacher labels JSONL")
	epochs := flag.Int("epochs", 25, "ranker training epochs")
	var snippets inputPaths
	flag.Var(&snippets, "snippets", "snippet JSONL file (repeatable)")
	flag.Parse()
	if *extract {
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
			*out = "scripts/activity-data/snippets.jsonl"
		}
		count, err := extractSnippets(codex, claude, *out, *limit, redactions...)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("extracted %d anonymized snippets to %s\n", count, *out)
		return
	}
	if len(snippets) == 0 {
		snippets = inputPaths{"scripts/activity-data/snippets.jsonl", "scripts/activity-data/claude-snippets.jsonl"}
	}
	if *out == "" {
		*out = "internal/controller/activity_ranker.bin"
	}
	if *epochs < 1 || strings.TrimSpace(*labels) == "" {
		fmt.Fprintln(os.Stderr, "-epochs must be positive and -labels must be set")
		os.Exit(2)
	}
	if err := trainRanker(snippets, *labels, *out, *epochs, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
