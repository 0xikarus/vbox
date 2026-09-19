package boxruntime

import (
	"slices"
	"testing"
)

func TestDesktopInputFits1024x640Screen(t *testing.T) {
	for _, tc := range []struct {
		x, y  int
		valid bool
	}{
		{0, 0, true},
		{1023, 639, true},
		{1024, 639, false},
		{1023, 640, false},
	} {
		err := validateDesktopAction(DesktopAction{Action: "move", X: tc.x, Y: tc.y})
		if (err == nil) != tc.valid {
			t.Errorf("move to (%d,%d): valid=%t, error=%v", tc.x, tc.y, tc.valid, err)
		}
	}
}

func TestDesktopBrowserStartsWithinScreenWithoutGPU(t *testing.T) {
	args := desktopChromiumArgs("/tmp/disposable-browser-profile", false)
	for _, flag := range []string{"--window-size=960,600", "--disable-gpu"} {
		if !slices.Contains(args, flag) {
			t.Errorf("browser startup missing %s", flag)
		}
	}
	if slices.Contains(args, "--process-per-site") {
		t.Fatal("browser startup must preserve the default site-isolation behavior")
	}
}
