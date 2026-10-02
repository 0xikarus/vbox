# Train your own activity and mood models

The repository ships synthetic-only training data and model artifacts. The
activity generator uses `scripts/activity-data/synthetic.jsonl`; the mood
classifier uses the short curated sentences in `scripts/mascot-data/`. Their
held-out scores measure synthetic label agreement, not real-world quality.

## Keep your data private

Work in a private directory outside this repository. Anonymization is only a
first pass: extracted text can retain box names, project identifiers, paths,
addresses, or credentials that patterns miss. Review every row before training
or sharing it. Do not commit your transcripts, labels, predictions, checkpoints,
or a model trained on private text to a public branch. A model can also memorize
parts of its training data.

## Extract and label activity examples

Run the extractor against your own Codex or Claude transcript directory. The
generator mode samples prose tails. Always supply an output path outside the
repository:

```sh
go run ./cmd/activity-train -extract -generator \
  -codex /private/codex/sessions -claude /private/claude/projects \
  -redact 'My Project' -out /private/activity-snippets.jsonl
```

The extractor writes JSONL with `id`, `text`, and optional `candidates`. Review and label
each retained row into a separate JSONL file containing `id`, `text`, and
`phrase`. Example with invented text:

```json
{"id":"demo-001","text":"assistant: Checking the build status","phrase":"Checking the build"}
```

A teacher prompt for private labeling can say: “Read only the provided agent
excerpt. Return one short present-participle verb phrase describing the agent's
current action, up to five words and 32 characters. Prefer the newest agent
action; do not infer intent from a user message or copy names, paths, secrets,
or old tool output. Skip an excerpt if the action is unclear. Preserve the
input ID and text, and output JSON with `id`, `text`, and `phrase`.” Review
the labels yourself. Keep repeated windows and related conversations together
when constructing a held-out set; the bundled trainer's stable ID-hash split
only guarantees disjoint IDs, not independence of related excerpts.

## Train and export an activity generator

Install the pinned CPU dependencies, then point the trainer at your reviewed
JSONL. `--synthetic` is the input flag name for historical compatibility; it
also accepts your private labeled file. Set every output to a private path:

```sh
python3 -m venv /private/activity-venv
/private/activity-venv/bin/pip install -r scripts/activity-model/requirements.txt
/private/activity-venv/bin/python \
  scripts/activity-model/train.py --synthetic /private/activity-labeled.jsonl \
  --out /private/activity_model.bin --golden /private/activity_golden.jsonl \
  --eval /private/activity-eval.json --predictions /private/activity-predictions.jsonl \
  --checkpoint /private/activity-best.pt --threads 1
```

Keep `go` on `PATH` so the trainer can run the normalizer. The trainer
normalizes the input, collapses exact duplicate normalized excerpts, holds out
about 20% of retained IDs, and uses about 10% for validation,
selects the best validation checkpoint, and exports float16 weights. Inspect
the held-out predictions yourself; token overlap with your labels is a limited
metric. The former real-transcript judge scores, including 84% good, are not
reproducible from this repository.

For the committed synthetic artifact, run `go run ./cmd/mascot-train -check`
for the mood model and these parity tests for the exported generator:

```sh
go test ./internal/activityphrase -run 'TestGeneratorArtifactChecksum|TestGeneratorMatchesPythonExport' -count=1
go test ./internal/controller -run 'TestActivityPhraseMatchesPythonFinalOutput' -count=1
```

To try your private generator in a private checkout, replace
`internal/controller/activity_model.bin`, copy its generated golden file into
`internal/activityphrase/testdata/activity_golden.jsonl`, and update
`generatorArtifactSHA256` in `internal/activityphrase/generator_test.go` to the
new artifact's SHA-256. Run the parity tests and `go build ./...`. Restore the
synthetic artifact and fixtures before publishing the branch.

## Train a mood classifier

The TSV format is one `label<TAB>text` row per example. Valid labels are
`idle`, `working`, `waiting`, `angry`, `happy`, and `laughing`. Keep training,
validation, and final holdout files separate. Train to a private output path:

```sh
go run ./cmd/mascot-train -train /private/mood-train.tsv \
  -eval /private/mood-validation.tsv -out /private/mascot_model.bin
go run ./cmd/mascot-train -train /private/mood-train.tsv \
  -eval /private/mood-validation.tsv -out /private/mascot_model.bin -check
```

Only after reviewing the separate holdout should you consider replacing
`internal/controller/mascot_model.bin` in a private checkout. Keep the private
training corpus and derived model outside the public repository.
