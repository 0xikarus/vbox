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
training only. Real training examples are repeated three times. Before training,
`cmd/activity-normalize` applies the same evidence normalization to all three
datasets: harness shell labels, generic tool names, repeated tool lines, trivial
commands, and screenshot sequences. The last two lines get a `<recent>` marker
in both the Python and Go tokenizers. The input is capped at 128 tokens to keep
the one-core inference tail below 50 ms. The best real validation loss selects the
checkpoint; exported float16 weights, vocabulary, and golden outputs are
generated from that checkpoint.

The evaluation JSON is written to `scripts/activity-model/eval.json` and is
intentionally local; `heldout-predictions.jsonl` contains all 557 real outputs
for semantic review. `--eval-only` regenerates both from the exported model.
The controller only shows model phrases with a mean emitted-token log probability
of at least -0.525 and with a short verb phrase that passes the grammar filter.
This threshold retained 70 of 317 judged v1 model outputs, with 7/70 (10.0%)
judged bad. To reproduce that calibration, extract the v1 artifact from commit
`3758202` and run `calibrate.py --model /path/to/v1.bin`. V1 judgements cannot
establish the semantic accuracy of new phrases from a retrained model; those need
a fresh judge. Set `--threads 1` to match
the one-core latency benchmark (`go test ./internal/activityphrase -run '^$'
-bench BenchmarkGenerator20Real -benchtime=1x`).
