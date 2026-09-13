// Package desktop implements worker-side desktop interaction primitives.
package desktop

import (
	"fmt"
	"math"
	"time"
)

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}
type MotionStep struct {
	Point
	At time.Duration
}

// Movement generates a bounded cubic Bezier path with eased timing. Bend is
// explicit so callers can use a seeded random source, or zero for precision work.
// All four control points are clamped, keeping the complete curve in the display.
func Movement(from, to Point, width, height int, bend float64) ([]MotionStep, error) {
	inside := func(p Point) bool { return p.X >= 0 && p.Y >= 0 && p.X < width && p.Y < height }
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 || !inside(from) || !inside(to) || math.IsNaN(bend) || math.IsInf(bend, 0) || math.Abs(bend) > 1 {
		return nil, fmt.Errorf("invalid cursor movement bounds")
	}
	dx, dy := float64(to.X-from.X), float64(to.Y-from.Y)
	distance := math.Hypot(dx, dy)
	if distance == 0 {
		return []MotionStep{{Point: to}}, nil
	}
	duration := time.Duration(math.Min(700, 60+distance*.35)) * time.Millisecond
	steps := int(math.Ceil(duration.Seconds() * 60))
	clamp := func(v float64, limit int) float64 { return math.Max(0, math.Min(float64(limit-1), v)) }
	c1x := clamp(float64(from.X)+dx/3-dy*bend*.12, width)
	c1y := clamp(float64(from.Y)+dy/3+dx*bend*.12, height)
	c2x := clamp(float64(from.X)+dx*2/3-dy*bend*.12, width)
	c2y := clamp(float64(from.Y)+dy*2/3+dx*bend*.12, height)
	curve := func(u float64) (float64, float64) {
		v := 1 - u
		return v*v*v*float64(from.X) + 3*v*v*u*c1x + 3*v*u*u*c2x + u*u*u*float64(to.X), v*v*v*float64(from.Y) + 3*v*v*u*c1y + 3*v*u*u*c2y + u*u*u*float64(to.Y)
	}
	// Approximate arc length, then ease distance rather than curve parameter.
	const samples = 256
	lengths := make([]float64, samples+1)
	px, py := curve(0)
	for i := 1; i <= samples; i++ {
		x, y := curve(float64(i) / samples)
		lengths[i] = lengths[i-1] + math.Hypot(x-px, y-py)
		px, py = x, y
	}
	out := make([]MotionStep, steps+1)
	segment := 1
	for i := range out {
		t := float64(i) / float64(steps)
		d := t * t * (3 - 2*t) * lengths[samples]
		for segment < samples && lengths[segment] < d {
			segment++
		}
		fraction := 0.0
		if span := lengths[segment] - lengths[segment-1]; span > 0 {
			fraction = (d - lengths[segment-1]) / span
		}
		u := (float64(segment-1) + fraction) / samples
		x, y := curve(u)
		out[i] = MotionStep{Point: Point{int(math.Round(x)), int(math.Round(y))}, At: time.Duration(float64(duration) * t)}
	}
	out[0].Point, out[len(out)-1].Point = from, to
	return out, nil
}
