# Mascot text classifier

The classifier accepts arbitrary text, with or without transcript role labels.
It considers the newest 8 KiB and weights the latest eight sentences most
heavily. The box MCP sender currently supplies native conversation text; its
adapter removes user requests and fenced code before sending the excerpt.
The controller classifies the received text directly and stores mood,
activity, and observation time; it does not store the excerpt. A stored mood
expires 40 seconds after the last observation.

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

The two holdout misses are a progress sentence about checks underway that the
model marked angry, and a stalled release that it marked happy. They show why
new real transcript examples and a fresh holdout set matter when improving the
model.

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

## Activity subtitle generator

The activity subtitle uses a separate, open-vocabulary pointer-generator in
`internal/controller/activity_model.bin`. Specific tool labels from the box
transcript take priority; otherwise the model proposes a short verb phrase.
The v3 controller also accepts lower-confidence prose-tail phrases when they
pass the recency and actor checks, with a first-person prose fallback. The
committed model weights are still v2's: its training set has 2,791 labeled real
snippets and 5,991 synthetic examples. Evaluation uses the same 557 real
held-out snippets throughout these comparisons.

For v3, 1,609 more labeled prose-ending snippets were collected: 109 from
current Codex sessions and 1,500 from a Claude session that also contributed
to the held-out set. Training filters duplicate IDs and near matches to the
validation or held-out 200-character tails or model inputs at 80% overlap. A
first run kept 1,013 extra examples; a second run also removed repeated windows
and capped each starting verb, leaving 751. Both gave the extra real examples
the same 3× training weight as the original real data and stopped on real
validation loss.

| Weights and display path | Phrases shown / 557 | Raw model overlap ≥0.6 / 557 |
| --- | ---: | ---: |
| v2 weights, v2 display rules | 194 | 188 |
| v3 unbalanced retrain, v3 display rules | 193 | 171 |
| v3 verb-balanced retrain, v3 display rules | 211 | 171 |
| v2 weights, v3 display rules | 223 | 188 |

The retrains generated more high-confidence “Waiting” phrases about earlier
steps or another box and showed less coverage than v2 weights with the same v3
display rules, so neither replaced the v2 weights.
On the v3 export, semantic review found 4 bad phrases among 223 shown (1.8%);
the 35 newly shown phrases contained about 25 good, 9 acceptable, and 1 bad.
The v3 export and 20 example pairs are in `scripts/activity-model/`.
