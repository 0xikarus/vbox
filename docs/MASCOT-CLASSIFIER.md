# Mascot text classifier

The classifier accepts the newest 8 KiB of a native agent transcript after the
box-side adapter removes user requests and fenced code. The controller stores
the derived mood, activity, and observation time; it does not store the text
sample. The stored mood expires 40 seconds after the last observation.

`internal/controller/mascot_model.bin` is a six-class linear model using word
unigrams, word bigrams, and character 3–5-grams. Its committed training,
development, and held-out TSV files under `scripts/mascot-data/` contain short,
curated synthetic status sentences, not captured conversation text. The
training set has 211 examples, development has 48, and held-out has 36.
The model scores 48/48 on development and 34/36 on held-out. These synthetic
scores do not measure performance on real agent conversations.

| Label | Mood | Activity |
| --- | --- | --- |
| idle | idle | idle |
| working | idle | working |
| waiting | waiting | waiting |
| angry | angry | idle |
| happy | happy | idle |
| laughing | laughing | idle |

Reproduce the committed artifact and check the synthetic holdout:

```sh
go run ./cmd/mascot-train -check
go run ./cmd/mascot-train -eval scripts/mascot-data/holdout.tsv -check
go test ./internal/controller -run 'TestClassifyMascotText|TestMascotModelHeldoutExamples'
```

The activity subtitle uses a separate pointer-generator in
`internal/controller/activity_model.bin`. Specific current tool labels take
priority; otherwise the model suggests a short verb phrase, subject to the
controller's confidence and validity gates. Its committed model and parity
fixtures now use only the synthetic corpus. Previous quality judgments on a
real-transcript held-out set, including the reported 84% good rate, are no
longer reproducible from this repository. The synthetic evaluation measures
agreement with synthetic labels only. See [the activity model README](../scripts/activity-model/README.md)
and [the training guide](TRAIN-ACTIVITY-MOOD.md).
