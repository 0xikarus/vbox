package boxruntime

import (
	"slices"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestRetainedDesktopResolutionControlsScreenAndInput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	if err := ConfigureDesktopResolution(WorkloadHome(), "1280x720"); err != nil {
		t.Fatal(err)
	}
	if width, height := desktopDimensions(); width != 1280 || height != 720 {
		t.Fatalf("screen=%dx%d", width, height)
	}
	if err := validateDesktopAction(DesktopAction{Action: "move", X: 1279, Y: 719}); err != nil {
		t.Fatal(err)
	}
	if err := validateDesktopAction(DesktopAction{Action: "move", X: 1280, Y: 719}); err == nil {
		t.Fatal("accepted position outside selected screen")
	}
	if !slices.Contains(desktopChromiumArgs("/tmp/disposable-browser-profile", false), "--window-size=1216,680") {
		t.Fatal("Chromium window did not follow selected screen")
	}
	if err := ConfigureDesktopResolution(WorkloadHome(), "1920x1080"); err == nil {
		t.Fatal("accepted unsupported resolution")
	}
	if err := v1.ValidateDesktopResolution(v1.DefaultDesktopResolution); err != nil {
		t.Fatal(err)
	}
}

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
