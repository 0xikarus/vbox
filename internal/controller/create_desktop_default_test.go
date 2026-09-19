package controller

import (
	"os"
	"strings"
	"testing"
)

// The desktop is a box default, not an optional checkbox. Keep sending its
// retained preset so a restored box can prepare desktop packages on any worker.
func TestWebCreationAlwaysIncludesDesktopWithoutCheckbox(t *testing.T) {
	for _, path := range []string{"web/app.js", "web/chat.js"} {
		t.Run(path, func(t *testing.T) {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			js := string(contents)
			if !strings.Contains(js, "if(preset.id==='desktop')continue") && !strings.Contains(js, "if(tool.id==='desktop')continue") {
				t.Fatal("desktop preset must not render as a creation checkbox")
			}
			if !strings.Contains(js, "tools=['desktop',") {
				t.Fatal("creation request must include the retained desktop preset")
			}
		})
	}
}
