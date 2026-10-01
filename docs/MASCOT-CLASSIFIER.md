# Mascot text classifier

The classifier accepts arbitrary text, with or without transcript role labels.
It considers the newest 8 KiB and weights the latest eight sentences most
heavily. The box MCP sender currently supplies native conversation text; its
adapter removes user requests and fenced code before calling the generic
classifier. The controller stores mood, activity, and observation time; it
does not store the excerpt.

## Model and labels

`internal/controller/mascot_model.bin` contains a six-class supervised linear
model. Its feature extractor uses word unigrams, word bigrams, and character
3–5-grams, hashed into 16,384 buckets. Training uses deterministic stochastic
gradient descent with a softmax loss. Inference runs in the existing Go
controller process with no additional runtime service. The model file is
393,252 bytes.

| Learned label | Mood | Activity |
| --- | --- | --- |
| idle | idle | idle |
| working | idle | working |
| waiting | waiting | waiting |
| angry | angry | idle |
| happy | happy | idle |
| laughing | laughing | idle |

The training corpus in `scripts/mascot-data/train.tsv` has 211 labeled status
examples. `scripts/mascot-data/eval.tsv` has 48 development examples used to
refine coverage. The model classifies all 48 correctly. A separate 36-example
holdout set, written after the model was trained, scores 34/36. These are
curated examples, not a measurement on real production transcripts; accuracy
in use may differ. Add reviewed, de-identified examples from real transcripts
before treating either score as a production quality estimate.

## Reproduce

From the repository root:

```sh
go run ./cmd/mascot-train
go run ./cmd/mascot-train -check
go run ./cmd/mascot-train -eval scripts/mascot-data/holdout.tsv -check
go test ./internal/controller -run 'TestClassifyMascotText|TestMascotModelHeldoutExamples'
go test ./internal/controller -run '^$' -bench '^BenchmarkClassifyMascotText$' -benchmem -count=3 -benchtime=1s
```

The training command overwrites the model file; `-check` verifies that the
committed artifact matches the training data. CI runs `-check` and the
classifier tests. The benchmark uses an 8 KiB excerpt and reports latency,
allocations, Go heap, and Linux process resident memory. Process RSS includes
the Go runtime and controller package initialization.

On the isolated test host, three runs of the generic classifier on an 8 KiB
plain-text excerpt took 0.185–0.190 ms per call, allocated 40,193 bytes in
156 allocations per call, and peaked at 19.4–20.0 MiB process RSS. Baseline
RSS was 15.7–16.3 MiB. These are process measurements, not a claim that the
classifier alone owns all of that memory.

When labels or examples change, retrain, inspect development examples, and add
a fresh set of unseen examples before reporting improved accuracy.
