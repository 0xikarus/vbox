#!/usr/bin/env python3
"""Calibrate a confidence gate against judged predictions from one model."""

import argparse
import importlib.util
import json
from pathlib import Path
import sys

import torch


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("activity_train", HERE / "train.py")
train = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = train
spec.loader.exec_module(train)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", type=Path, required=True, help="export that produced the judged predictions")
    parser.add_argument("--data", type=Path, default=HERE.parent / "activity-data/synthetic.jsonl")
    parser.add_argument("--predictions", type=Path, required=True)
    parser.add_argument("--judgements", type=Path, required=True)
    parser.add_argument("--threshold", type=float, default=-.525)
    args = parser.parse_args()
    torch.set_num_threads(1)
    normalized = train.normalized_paths(args.data, Path.home() / ".cache/activity-calibrate")
    _, _, heldout = train.load_data(normalized)
    predictions = {item["id"]: item for item in train.read_jsonl(args.predictions)}
    judgements = {item["id"]: item["verdict"] for item in train.read_jsonl(args.judgements)}
    model, vocab = train.load_export(args.model)
    scored = []
    for item in heldout:
        old = predictions[item.id]
        if old["source"] != "model":
            continue
        phrase, score = train.predict_scored(model, item, vocab)
        if phrase != old["output"]:
            raise ValueError(f"{item.id}: export does not reproduce judged prediction")
        scored.append((score, judgements[item.id], train.valid_phrase(phrase)))
    shown = [row for row in scored if row[0] >= args.threshold and row[2]]
    bad = sum(row[1] == "bad" for row in shown)
    print(f"judged model outputs: {len(scored)}; shown: {len(shown)}; bad: {bad}/{len(shown)} ({bad/len(shown):.1%}); model coverage: {len(shown)/len(scored):.1%}")
    best = None
    for threshold in sorted({row[0] for row in scored}, reverse=True):
        accepted = [row for row in scored if row[0] >= threshold and row[2]]
        if accepted and sum(row[1] == "bad" for row in accepted) / len(accepted) <= .1:
            if best is None or len(accepted) > best[1]:
                best = (threshold, len(accepted))
    print(f"maximum coverage at <=10% bad: threshold {best[0]:.6f}, shown {best[1]}/{len(scored)}")


if __name__ == "__main__":
    main()
