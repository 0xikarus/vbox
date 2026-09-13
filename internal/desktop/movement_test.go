package desktop

import (
	"math"
	"testing"
	"time"
)

func TestMovementBoundsAndTiming(t *testing.T) {
	points := []Point{{0, 0}, {1279, 799}, {0, 799}, {1279, 0}, {640, 400}}
	for _, from := range points {
		for _, to := range points {
			for _, bend := range []float64{-1, 0, 1} {
				path, err := Movement(from, to, 1280, 800, bend)
				if err != nil {
					t.Fatal(err)
				}
				if path[0].Point != from || path[len(path)-1].Point != to {
					t.Fatal("incorrect endpoint")
				}
				for i, step := range path {
					if step.X < 0 || step.Y < 0 || step.X >= 1280 || step.Y >= 800 {
						t.Fatal("left screen")
					}
					if step.At > 700*time.Millisecond || (i > 0 && step.At <= path[i-1].At) {
						t.Fatal("invalid timing")
					}
				}
			}
		}
	}
}

func TestMovementRejectsInvalidInput(t *testing.T) {
	for _, bend := range []float64{2, math.NaN(), math.Inf(1)} {
		if _, err := Movement(Point{}, Point{10, 10}, 1280, 800, bend); err == nil {
			t.Fatal("accepted invalid bend")
		}
	}
	if _, err := Movement(Point{}, Point{1280, 0}, 1280, 800, 0); err == nil {
		t.Fatal("accepted offscreen target")
	}
}
