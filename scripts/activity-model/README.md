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
`cmd/activity-normalize` passes synthetic shell tool lines through the same
label parser used by the box harness. The best real validation loss selects the
checkpoint; exported float16 weights, vocabulary, and golden outputs are
generated from that checkpoint.

The evaluation JSON is written to `scripts/activity-model/eval.json` and is
intentionally local; check the reported held-out overlap, exact match, and
example pairs before updating the checked-in model. Set `--threads 1` to match
the one-core latency benchmark (`go test ./internal/activityphrase -run '^$'
-bench BenchmarkGenerator20Real -benchtime=1x`).
