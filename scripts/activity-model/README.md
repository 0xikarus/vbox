# Activity phrase generator

The controller embeds `internal/controller/activity_model.bin` and runs the
pointer-generator in pure Go. The committed model is trained only on
`scripts/activity-data/synthetic.jsonl`. That file has 5,991 synthetic examples
with `id`, `text`, and `phrase` fields. Exact duplicate normalized inputs are
collapsed before splitting, leaving 5,805 unique examples. A stable SHA-256
split of each retained ID keeps 4,052 for training, 576 for validation, and
1,177 for held-out evaluation. Held-out inputs never enter training or
vocabulary building.

Install the pinned CPU dependencies in a local virtual environment, then train:

```sh
python3 -m venv /tmp/activity-model-venv
/tmp/activity-model-venv/bin/pip install -r scripts/activity-model/requirements.txt
/tmp/activity-model-venv/bin/python scripts/activity-model/train.py --threads 1
```

The trainer first runs `cmd/activity-normalize` on its input. Its best validation
checkpoint exports float16 weights and vocabulary. The 50 checked-in Python/Go
golden inputs and 20 benchmark inputs come from the synthetic held-out split.
`scripts/activity-model/eval.json` and `predictions.jsonl` stay local and are
ignored by Git. `--eval-only` regenerates them from the exported artifact.
CI verifies the mascot model, model checksum, and Python/Go golden parity
without installing PyTorch.

On this synthetic split, the new model's final output overlaps its label by at
least 0.6 in **28/1,177 (2.4%)** cases, and emits a phrase in 534/1,177. The
previous model gives 40/1,177 overlap and 547/1,177 coverage on these same
rows, but it was trained on all 5,991 synthetic rows, including these 1,177;
that comparison is in-sample for the old model and cannot establish a quality
gap. The previous real-transcript held-out judgments, including the reported
84% good rate, cannot be reproduced from this repository. Synthetic label
overlap is not a production quality estimate. See [Train your own activity and mood models](../../docs/TRAIN-ACTIVITY-MOOD.md)
for private extraction, labeling, training, export, and verification.
