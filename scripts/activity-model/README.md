# Activity phrase generator

This trainer is offline. The controller embeds `internal/controller/activity_model.bin`
and runs the exported pointer-generator in pure Go. CI checks the checked-in
artifact and 50 Python/Go golden outputs; it does not install PyTorch or train.

Install the pinned CPU dependencies in a local virtual environment, then train:

```sh
python3 -m venv /tmp/activity-model-venv
/tmp/activity-model-venv/bin/pip install -r scripts/activity-model/requirements.txt
/tmp/activity-model-venv/bin/python scripts/activity-model/train.py
```

The trainer joins `scripts/activity-data/{snippets,claude-snippets}.jsonl` with
`labels.jsonl` by ID. SHA-256 of each real ID assigns 20% to held-out evaluation,
10% to validation, and 70% to training. All examples in `synthetic.jsonl` are
training only. The optional v3 `generator-v3-labeled.jsonl` prose examples can
be passed with `--extra-real scripts/activity-data/generator-v3-labeled.jsonl`.
The trainer then removes duplicate IDs, repeated or near-identical windows,
and examples with at least 80% overlap with a validation or held-out
200-character tail or model input. These examples never enter validation or
evaluation. Real training examples, including any clean additions, are repeated
three times. Before training, `cmd/activity-normalize` applies the same evidence
normalization to every dataset: harness shell labels, generic tool names,
repeated tool lines, trivial
commands, and screenshot sequences. The last two lines get a `<recent>` marker
in both the Python and Go tokenizers. The input is capped at 128 tokens to keep
the one-core inference tail below 50 ms. The best real validation loss selects the
checkpoint; exported float16 weights, vocabulary, and golden outputs are
generated from that checkpoint.

The evaluation JSON is written to `scripts/activity-model/eval.json` and is
intentionally local; `heldout-predictions.jsonl` contains all 557 real outputs
for semantic review. `--eval-only` regenerates both from the exported model.
The controller shows specific current tool labels first. For model phrases it
keeps the v2 confidence gate of -0.525, allows a lower -0.65 gate for a prose
tail if the phrase is not Waiting/Awaiting, and uses an explicit first-person
prose action when the model is gated out. All outputs pass the short verb-phrase
filter. The v2 judge found three bad among 194 shown phrases. Those judgements
do not establish the semantic accuracy of newly displayed v3 phrases;
the exported predictions need a fresh judge. Set `--threads 1` to match
the one-core latency benchmark (`go test ./internal/activityphrase -run '^$'
-bench BenchmarkGenerator20Real -benchtime=1x`).

The v3 prose labels were evaluated as an experiment before selecting live
weights. The unbalanced run showed 193/557 phrases, and a verb-balanced run
showed 211/557. Both trailed the bundled v2 weights plus the v3 display gates,
which showed 223/557 with its actor guard, so the v3 branch retains the v2 model
artifact. Reuse the extra corpus only when a new held-out run improves both
coverage and quality.
