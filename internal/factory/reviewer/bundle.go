package reviewer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrIncomplete prevents any approval when actual changed content cannot be
// supplied. Additional read-only model tools are not a substitute for this gate.
var ErrIncomplete = errors.New("unsupported/incomplete source review")

func sourceBundle(run func(...string) (string, error), base, candidate string) (string, error) {
	names, err := run("diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", base, candidate, "--")
	if err != nil {
		return "", fmt.Errorf("%w: changed path inventory: %v", ErrIncomplete, err)
	}
	changed := map[string]bool{}
	for _, name := range strings.Split(names, "\x00") {
		changed[name] = true
	}
	var source strings.Builder
	source.WriteString("Committed base/candidate inventories: mode, type, Git object SHA, byte size, quoted path, content disposition. Unchanged contents omitted (not inspected); read-only tools may inspect additional context. Changed contents below are actual committed bytes.\n")
	for _, revision := range []struct{ label, sha string }{{"BASE", base}, {"CANDIDATE", candidate}} {
		tree, err := run("ls-tree", "-rlz", revision.sha)
		if err != nil {
			return "", fmt.Errorf("%w: %s inventory: %v", ErrIncomplete, revision.label, err)
		}
		source.WriteString("\n" + revision.label + " INVENTORY AND CHANGED CONTENTS\n")
		for _, entry := range strings.Split(tree, "\x00") {
			if entry == "" {
				continue
			}
			meta, name, ok := strings.Cut(entry, "\t")
			fields := strings.Fields(meta)
			if !ok || len(fields) != 4 {
				return "", fmt.Errorf("%w: invalid tree entry", ErrIncomplete)
			}
			fmt.Fprintf(&source, "\n%s %q", meta, name)
			if !changed[name] {
				source.WriteString(" CONTENT OMITTED: unchanged; text/binary classification not inspected\n")
			} else {
				size, e := strconv.ParseInt(fields[3], 10, 64)
				if fields[1] != "blob" || e != nil || size > maxSource {
					return "", fmt.Errorf("%w: %s %q content omitted: unsupported type or exceeds 512 KiB", ErrIncomplete, revision.label, name)
				}
				content, e := run("cat-file", "blob", fields[2])
				if e != nil {
					return "", fmt.Errorf("%w: %s %q content unavailable: %v", ErrIncomplete, revision.label, name, e)
				}
				if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
					return "", fmt.Errorf("%w: %s %q content omitted: binary/non-UTF-8", ErrIncomplete, revision.label, name)
				}
				fmt.Fprintf(&source, " CONTENT INCLUDED (%d bytes)\n%s\nEND CONTENT\n", len(content), content)
			}
			if source.Len() > maxSource {
				return "", fmt.Errorf("%w: inventory and changed contents exceed 512 KiB", ErrIncomplete)
			}
		}
	}
	// Force text diff only after validating both changed sides as text. Repository
	// diff attributes cannot suppress actual hunks or invoke a textconv driver.
	diff, err := run("diff", "--text", "--no-ext-diff", "--no-textconv", "--no-renames", base, candidate, "--")
	if err != nil {
		return "", fmt.Errorf("%w: actual diff unavailable: %v", ErrIncomplete, err)
	}
	source.WriteString("\nACTUAL BASE-TO-CANDIDATE GIT DIFF\n" + diff)
	if source.Len() > maxSource {
		return "", fmt.Errorf("%w: inventory, changed contents and diff exceed 512 KiB", ErrIncomplete)
	}
	return source.String(), nil
}
