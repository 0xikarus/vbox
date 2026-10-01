package activityphrase

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
	"time"
)

const generatorArtifactSHA256 = "ef43e1e311da14e4d3f2a9cab373e76becf8f9101afa7a58a9906ca644dd89cd"

type generatorGolden struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Phrase string  `json:"phrase"`
	Score  float64 `json:"score"`
}

func loadGeneratorRows(t testing.TB, path string) []generatorGolden {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []generatorGolden
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var row generatorGolden
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func loadGeneratorGoldens(t testing.TB) []generatorGolden {
	return loadGeneratorRows(t, "testdata/activity_golden.jsonl")
}

func loadGeneratorArtifact(t testing.TB) *Generator {
	t.Helper()
	data, err := os.ReadFile("../controller/activity_model.bin")
	if err != nil {
		t.Fatal(err)
	}
	model, err := LoadGenerator(data)
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestGeneratorArtifactChecksum(t *testing.T) {
	data, err := os.ReadFile("../controller/activity_model.bin")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != generatorArtifactSHA256 {
		t.Fatalf("generator artifact checksum = %s, want %s", got, generatorArtifactSHA256)
	}
}

func TestGeneratorMatchesPythonExport(t *testing.T) {
	model := loadGeneratorArtifact(t)
	goldens := loadGeneratorGoldens(t)
	if len(goldens) != 50 {
		t.Fatalf("want 50 golden inputs, got %d", len(goldens))
	}
	for _, golden := range goldens {
		got, score := model.GenerateScored(golden.Text)
		if got != golden.Phrase {
			t.Errorf("%s: Go %q, Python %q", golden.ID, got, golden.Phrase)
		}
		if got != "" && math.Abs(score-golden.Score) > 1e-3 {
			t.Errorf("%s: Go score %.6f, Python %.6f", golden.ID, score, golden.Score)
		}
	}
}

func BenchmarkGenerator20Real(b *testing.B) {
	model := loadGeneratorArtifact(b)
	goldens := loadGeneratorRows(b, "testdata/activity_benchmark.jsonl")
	if len(goldens) < 20 {
		b.Fatal("need at least 20 real golden inputs")
	}
	durations := make([]time.Duration, 0, b.N*20)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for _, row := range goldens[:20] {
			started := time.Now()
			_ = model.Generate(row.Text)
			durations = append(durations, time.Since(started))
		}
	}
	b.StopTimer()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	b.ReportMetric(float64(durations[len(durations)/2])/float64(time.Millisecond), "p50_ms")
	b.ReportMetric(float64(durations[(len(durations)*95+99)/100-1])/float64(time.Millisecond), "p95_ms")
}
