package planner

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

// Opt-in actual installed CLI test, entirely separate from controlled fixtures.
// Ground truth is encoded only in generated pixels, never the filename or prompt.
func TestLiveCodexImageGrounding(t *testing.T) {
	if os.Getenv("PLANNER_LIVE_CODEX") != "1" {
		t.Skip("opt-in real Codex test")
	}
	r := request(t, "codex")
	if err := exec.Command("git", "-C", r.Workspace, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 600, 300))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(50, 70, 190, 210), image.NewUniform(color.RGBA{255, 0, 0, 255}), image.Point{}, draw.Src)
	for y := 60; y < 220; y++ {
		for x := 350; x < 510; x++ {
			dx, dy := x-430, y-140
			if dx*dx+dy*dy < 80*80 {
				img.Set(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	p := filepath.Join(t.TempDir(), "attachment.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r.Images = []string{p}
	r.Prompt = "Describe the colors, shapes, and left-to-right arrangement visible in the attached image in response. Propose a short plan to recreate it. Do not read files or run tools; use the attached visual input only."
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := Run(ctx, r)
	if err != nil {
		t.Fatalf("real Codex failure: exit=%v signal=%d truncated=%v error=%v", exitText(result), result.Signal, result.Truncated, err)
	}
	parsed, err := factory.ParsePlannerResult(result.Document)
	if err != nil {
		t.Fatal(err)
	}
	response := strings.ToLower(parsed.Response)
	for _, word := range []string{"red", "square", "left", "blue", "circle", "right"} {
		if !strings.Contains(response, word) {
			t.Fatalf("image grounding missing %s: %s", word, parsed.Response)
		}
	}
	t.Logf("REAL Codex exit=%v signal=%d bytes=%d; response=%s", exitText(result), result.Signal, len(result.Document), parsed.Response)
}
func exitText(r Result) any {
	if r.ExitCode == nil {
		return nil
	}
	return *r.ExitCode
}
func TestLiveClaudeText(t *testing.T) {
	if os.Getenv("PLANNER_LIVE_CLAUDE") != "1" {
		t.Skip("opt-in real Claude test")
	}
	r := request(t, "claude")
	r.Prompt = "Propose a one-sentence plan for a hello-world application; no tools needed."
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	got, err := Run(ctx, r)
	if err != nil {
		t.Fatalf("REAL Claude exit=%v signal=%d: %v", exitText(got), got.Signal, err)
	}
	t.Logf("REAL Claude exit=%v bytes=%d", exitText(got), len(got.Document))
}
