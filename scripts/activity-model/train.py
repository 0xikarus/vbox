#!/usr/bin/env python3
"""Train, export, and evaluate the local activity pointer-generator.

The only runtime artifact is activity_model.bin. Training uses PyTorch on a
developer machine; the controller's decoder is pure Go.
"""

import argparse
import array
import collections
import hashlib
import json
import math
import random
import re
import shutil
import struct
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

import torch
from torch import nn
from torch.nn.utils.rnn import pack_padded_sequence, pad_packed_sequence


ROOT = Path(__file__).resolve().parents[2]
MAGIC = b"ACTGEN01"
TOKEN = re.compile(r"#?[\w]+(?:[._'-][\w]+)*", re.UNICODE)
OVERLAP_TOKEN = re.compile(r"[^\W_]+", re.UNICODE)
SPECIAL = ["<pad>", "<bos>", "<eos>", "<unk>", "<sep>"]
PAD, BOS, EOS, UNK, SEP = range(5)
MAX_INPUT = 192
MAX_OUTPUT = 8
EMBED = 128
ENC_HIDDEN = 128
DEC_HIDDEN = 192
VOCAB_LIMIT = 8192


@dataclass(frozen=True)
class Sample:
    id: str
    text: str
    phrase: str
    source: str


def read_jsonl(path):
    with open(path, encoding="utf-8") as source:
        for line in source:
            if line.strip():
                yield json.loads(line)


def load_data(synthetic_path):
    snippets = {}
    for name in ("snippets.jsonl", "claude-snippets.jsonl"):
        for item in read_jsonl(ROOT / "scripts/activity-data" / name):
            snippets[item["id"]] = item["text"]
    labels = {item["id"]: item["phrase"] for item in read_jsonl(ROOT / "scripts/activity-data/labels.jsonl")}
    if snippets.keys() != labels.keys():
        raise ValueError("real snippets and labels have different IDs")
    real = [Sample(key, snippets[key], labels[key], "real") for key in sorted(snippets)]
    train, validation, heldout = [], [], []
    for item in real:
        bucket = hashlib.sha256(item.id.encode()).digest()[0]
        if bucket % 5 == 0:
            heldout.append(item)
        elif bucket % 10 == 1:
            validation.append(item)
        else:
            train.append(item)
    synthetic = []
    if synthetic_path.exists():
        synthetic = [Sample(item["id"], item["text"], item["phrase"], "synthetic") for item in read_jsonl(synthetic_path)]
        real_ids = {item.id for item in real}
        if any(item.id in real_ids for item in synthetic):
            raise ValueError("synthetic IDs overlap real evaluation data")
    return train, validation, heldout, synthetic


def normalize_synthetic(input_path, output_path):
    if not input_path.exists():
        return input_path
    go = shutil.which("go") or "/data/go/bin/go"
    subprocess.run([go, "run", "./cmd/activity-normalize", "-input", str(input_path), "-output", str(output_path)], cwd=ROOT, check=True)
    return output_path


def source_tokens(text):
    lines = [line.strip() for line in text.splitlines() if line.strip() and not line.startswith("user: ")]
    tokens = []
    for line in lines[-6:]:
        if tokens:
            tokens.append("<sep>")
        tokens.extend(TOKEN.findall(line.lower()))
    return tokens[-MAX_INPUT:] or ["<unk>"]


def phrase_tokens(text):
    return TOKEN.findall(text.lower())[:MAX_OUTPUT]


def display_phrase(tokens):
    phrase = " ".join(tokens).strip()
    return phrase[:1].upper() + phrase[1:] if phrase else ""


def latest_tool_label(text):
    lines = text.strip().splitlines()
    if not lines or not lines[-1].strip().startswith("tool: "):
        return ""
    label = lines[-1].strip()[6:]
    return label if label != "Running tool" else ""


def valid_phrase(phrase):
    words = phrase.split()
    return (1 <= len(words) <= 6 and len(phrase) <= 32 and phrase[:1].isupper()
            and len({word.lower() for word in words}) == len(words)
            and all(not any(char in word for char in "<>\n\r") for word in words))


def token_overlap(left, right):
    a = set(OVERLAP_TOKEN.findall(left.lower()))
    b = set(OVERLAP_TOKEN.findall(right.lower()))
    return 2 * len(a & b) / (len(a) + len(b)) if a and b else 0.0


def build_vocab(training):
    counts = collections.Counter()
    for item in training:
        counts.update(source_tokens(item.text))
        counts.update(phrase_tokens(item.phrase))
    words = [word for word, _ in sorted(counts.items(), key=lambda pair: (-pair[1], pair[0])) if word not in SPECIAL]
    return SPECIAL + words[: VOCAB_LIMIT - len(SPECIAL)]


def encode_sample(item, word_to_id):
    source = source_tokens(item.text)
    unknowns = []
    unknown_ids = {}
    src_ids, extended = [], []
    for token in source:
        known = word_to_id.get(token, UNK)
        src_ids.append(known)
        if known == UNK and token != "<unk>":
            if token not in unknown_ids:
                unknown_ids[token] = len(word_to_id) + len(unknowns)
                unknowns.append(token)
            extended.append(unknown_ids[token])
        else:
            extended.append(known)
    target = phrase_tokens(item.phrase) + ["<eos>"]
    target_ids = [word_to_id.get(token, unknown_ids.get(token, UNK)) for token in target]
    previous = [BOS] + [word_to_id.get(token, UNK) for token in target[:-1]]
    return item, src_ids, extended, target_ids, previous, unknowns


def collate(rows, device):
    batch = len(rows)
    src_len = max(len(row[1]) for row in rows)
    tgt_len = max(len(row[3]) for row in rows)
    src = torch.full((batch, src_len), PAD, dtype=torch.long, device=device)
    ext = torch.full((batch, src_len), PAD, dtype=torch.long, device=device)
    target = torch.full((batch, tgt_len), PAD, dtype=torch.long, device=device)
    previous = torch.full((batch, tgt_len), PAD, dtype=torch.long, device=device)
    lengths = []
    for i, row in enumerate(rows):
        lengths.append(len(row[1]))
        src[i, : len(row[1])] = torch.tensor(row[1], dtype=torch.long, device=device)
        ext[i, : len(row[2])] = torch.tensor(row[2], dtype=torch.long, device=device)
        target[i, : len(row[3])] = torch.tensor(row[3], dtype=torch.long, device=device)
        previous[i, : len(row[4])] = torch.tensor(row[4], dtype=torch.long, device=device)
    max_oov = max(len(row[5]) for row in rows)
    return src, ext, target, previous, lengths, max_oov


class PointerGenerator(nn.Module):
    def __init__(self, vocab_size, embed=EMBED, enc_hidden=ENC_HIDDEN, dec_hidden=DEC_HIDDEN):
        super().__init__()
        self.vocab_size = vocab_size
        self.embed_size = embed
        self.enc_hidden = enc_hidden
        self.dec_hidden = dec_hidden
        self.embedding = nn.Embedding(vocab_size, embed, padding_idx=PAD)
        self.encoder = nn.GRU(embed, enc_hidden, batch_first=True, bidirectional=True)
        self.init_proj = nn.Linear(enc_hidden * 2, dec_hidden)
        self.attn_enc = nn.Linear(enc_hidden * 2, dec_hidden, bias=False)
        self.attn_dec = nn.Linear(dec_hidden, dec_hidden, bias=False)
        self.attn_v = nn.Linear(dec_hidden, 1, bias=False)
        self.decoder = nn.GRUCell(embed + enc_hidden * 2, dec_hidden)
        self.gen_proj = nn.Linear(dec_hidden + enc_hidden * 2, embed)
        self.gen_bias = nn.Parameter(torch.zeros(vocab_size))
        self.p_gen = nn.Linear(embed + enc_hidden * 2 + dec_hidden, 1)

    def encode(self, src, lengths):
        packed = pack_padded_sequence(self.embedding(src), lengths, batch_first=True, enforce_sorted=False)
        packed_out, hidden = self.encoder(packed)
        encoded, _ = pad_packed_sequence(packed_out, batch_first=True, total_length=src.shape[1])
        state = torch.tanh(self.init_proj(torch.cat((hidden[0], hidden[1]), dim=1)))
        projected = self.attn_enc(encoded)
        mask = src.ne(PAD)
        return encoded, projected, state, mask

    def step(self, previous, state, encoded, projected, mask, ext, max_oov):
        embedded = self.embedding(previous)
        attention = self.attn_v(torch.tanh(projected + self.attn_dec(state).unsqueeze(1))).squeeze(-1)
        attention = torch.softmax(attention.masked_fill(~mask, -1e9), dim=1)
        context = torch.bmm(attention.unsqueeze(1), encoded).squeeze(1)
        state = self.decoder(torch.cat((embedded, context), dim=1), state)
        projected_out = torch.tanh(self.gen_proj(torch.cat((state, context), dim=1)))
        vocab = torch.softmax(torch.nn.functional.linear(projected_out, self.embedding.weight, self.gen_bias), dim=1)
        generation = torch.sigmoid(self.p_gen(torch.cat((embedded, context, state), dim=1)))
        final = torch.nn.functional.pad(generation * vocab, (0, max_oov))
        final.scatter_add_(1, ext, (1 - generation) * attention)
        return final, state

    def forward(self, src, ext, target, previous, lengths, max_oov):
        encoded, projected, state, mask = self.encode(src, lengths)
        losses = []
        for time in range(target.shape[1]):
            probs, state = self.step(previous[:, time], state, encoded, projected, mask, ext, max_oov)
            wanted = target[:, time]
            nll = -torch.log(probs.gather(1, wanted.unsqueeze(1)).squeeze(1).clamp_min(1e-9))
            losses.append(nll * wanted.ne(PAD))
        return torch.stack(losses, dim=1).sum() / target.ne(PAD).sum().clamp_min(1)


@torch.no_grad()
def predict(model, item, vocab, device="cpu"):
    word_to_id = {word: index for index, word in enumerate(vocab)}
    encoded_item = encode_sample(item, word_to_id)
    src, ext, _, _, lengths, max_oov = collate([encoded_item], device)
    model.eval()
    encoded, projected, state, mask = model.encode(src, lengths)
    previous = torch.tensor([BOS], dtype=torch.long, device=device)
    generated = []
    for _ in range(MAX_OUTPUT):
        probs, state = model.step(previous, state, encoded, projected, mask, ext, max_oov)
        probs[:, [PAD, BOS, UNK, SEP]] = -1
        chosen = int(probs.argmax(dim=1).item())
        if chosen == EOS:
            break
        if chosen < len(vocab):
            generated.append(vocab[chosen])
            previous[0] = chosen
        else:
            generated.append(encoded_item[5][chosen - len(vocab)])
            previous[0] = UNK
    return display_phrase(generated)


def export_model(model, vocab, path):
    with open(path, "wb") as output:
        output.write(MAGIC)
        output.write(struct.pack("<HHHHHI", model.embed_size, model.enc_hidden, model.dec_hidden, MAX_INPUT, MAX_OUTPUT, len(vocab)))
        tensors = sorted(model.state_dict().items())
        output.write(struct.pack("<I", len(tensors)))
        for token in vocab:
            raw = token.encode()
            output.write(struct.pack("<H", len(raw)))
            output.write(raw)
        for name, tensor in tensors:
            encoded = name.encode()
            output.write(struct.pack("<B", len(encoded)))
            output.write(encoded)
            output.write(struct.pack("<B", tensor.ndim))
            for dimension in tensor.shape:
                output.write(struct.pack("<I", dimension))
            values = tensor.detach().cpu().to(torch.float16).contiguous().view(torch.int16).flatten().tolist()
            if sys.byteorder != "little":
                raise RuntimeError("export requires little-endian byte order")
            output.write(array.array("h", values).tobytes())


def load_export(path):
    with open(path, "rb") as source:
        if source.read(8) != MAGIC:
            raise ValueError("invalid generator artifact")
        embed, enc_hidden, dec_hidden, max_input, max_output, vocab_size = struct.unpack("<HHHHHI", source.read(14))
        if max_input != MAX_INPUT or max_output != MAX_OUTPUT:
            raise ValueError("unsupported generator limits")
        count, = struct.unpack("<I", source.read(4))
        vocab = []
        for _ in range(vocab_size):
            length, = struct.unpack("<H", source.read(2))
            vocab.append(source.read(length).decode())
        state = {}
        for _ in range(count):
            length, = struct.unpack("<B", source.read(1))
            name = source.read(length).decode()
            ndim, = struct.unpack("<B", source.read(1))
            shape = [struct.unpack("<I", source.read(4))[0] for _ in range(ndim)]
            values = source.read(math.prod(shape) * 2)
            state[name] = torch.frombuffer(bytearray(values), dtype=torch.float16).float().reshape(shape).clone()
        if source.read(1):
            raise ValueError("trailing artifact bytes")
    model = PointerGenerator(vocab_size, embed, enc_hidden, dec_hidden)
    model.load_state_dict(state)
    model.eval()
    return model, vocab


def batches(rows, batch_size, seed):
    randomizer = random.Random(seed)
    ordered = rows[:]
    randomizer.shuffle(ordered)
    groups = []
    for start in range(0, len(ordered), 512):
        window = sorted(ordered[start:start + 512], key=lambda row: len(row[1]))
        groups.extend(window[i:i + batch_size] for i in range(0, len(window), batch_size))
    randomizer.shuffle(groups)
    return groups


def evaluate(model, rows, vocab, output_path, golden_path):
    pairs = []
    golden = []
    for item in rows:
        generated = predict(model, item, vocab)
        label = latest_tool_label(item.text)
        final = label or (generated if valid_phrase(generated) else "")
        pairs.append({"id": item.id, "input_tail": "\n".join(item.text.splitlines()[-2:]), "output": final, "model": generated, "teacher": item.phrase})
        if len(golden) < 50:
            golden.append({"id": item.id, "text": item.text, "phrase": generated})
    hits = sum(token_overlap(pair["output"], pair["teacher"]) >= .6 for pair in pairs)
    exact = sum(pair["output"] == pair["teacher"] for pair in pairs)
    model_hits = sum(token_overlap(pair["model"], pair["teacher"]) >= .6 for pair in pairs)
    coverage = sum(bool(pair["output"]) for pair in pairs)
    report = {"heldout": len(rows), "top1_hits": hits, "exact": exact, "coverage": coverage, "model_only_hits": model_hits, "examples": pairs[:20]}
    output_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    with open(golden_path, "w", encoding="utf-8") as output:
        for item in golden:
            output.write(json.dumps(item, ensure_ascii=False) + "\n")
    print(f"REAL held-out: overlap>=0.6 {hits}/{len(rows)} ({hits/len(rows):.1%}), exact {exact}/{len(rows)}, coverage {coverage}/{len(rows)}, model-only {model_hits}/{len(rows)}")
    for pair in pairs[:20]:
        print(f"  {pair['id']}: {pair['input_tail'][-70:]!r} -> {pair['output']!r} (teacher {pair['teacher']!r})")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--synthetic", type=Path, default=ROOT / "scripts/activity-data/synthetic.jsonl")
    parser.add_argument("--out", type=Path, default=ROOT / "internal/controller/activity_model.bin")
    parser.add_argument("--golden", type=Path, default=ROOT / "internal/activityphrase/testdata/activity_golden.jsonl")
    parser.add_argument("--eval", type=Path, default=ROOT / "scripts/activity-model/eval.json")
    parser.add_argument("--epochs", type=int, default=20)
    parser.add_argument("--patience", type=int, default=3)
    parser.add_argument("--batch-size", type=int, default=32)
    parser.add_argument("--threads", type=int, default=1)
    parser.add_argument("--max-train-batches", type=int, default=0, help="smoke-test limit; zero trains every batch")
    parser.add_argument("--skip-eval", action="store_true", help="skip held-out decoding during a smoke test")
    parser.add_argument("--checkpoint", type=Path, default=Path.home() / ".cache/activity-generator-best.pt")
    args = parser.parse_args()
    torch.set_num_threads(args.threads)
    torch.manual_seed(20261001)
    random.seed(20261001)
    args.checkpoint.parent.mkdir(parents=True, exist_ok=True)
    normalized_synthetic = normalize_synthetic(args.synthetic, args.checkpoint.with_name("activity-synthetic-normalized.jsonl"))
    train, validation, heldout, synthetic = load_data(normalized_synthetic)
    vocab = build_vocab(train + synthetic)
    word_to_id = {word: index for index, word in enumerate(vocab)}
    encoded_train = [encode_sample(item, word_to_id) for item in train for _ in range(3)]
    encoded_train += [encode_sample(item, word_to_id) for item in synthetic]
    encoded_validation = [encode_sample(item, word_to_id) for item in validation]
    model = PointerGenerator(len(vocab))
    optimizer = torch.optim.AdamW(model.parameters(), lr=.001)
    best_loss, stale = float("inf"), 0
    print(f"real train/validation/held-out {len(train)}/{len(validation)}/{len(heldout)}, synthetic {len(synthetic)}, vocab {len(vocab)}", flush=True)
    for epoch in range(args.epochs):
        model.train()
        total = 0.0
        trained_rows = 0
        for batch_index, group in enumerate(batches(encoded_train, args.batch_size, 20261001 + epoch)):
            if args.max_train_batches and batch_index >= args.max_train_batches:
                break
            batch = collate(group, "cpu")
            optimizer.zero_grad(set_to_none=True)
            loss = model(*batch)
            loss.backward()
            nn.utils.clip_grad_norm_(model.parameters(), 1.0)
            optimizer.step()
            total += float(loss.detach()) * len(group)
            trained_rows += len(group)
            if (batch_index + 1) % 25 == 0:
                print(f"epoch {epoch + 1} batch {batch_index + 1}: train loss {total/trained_rows:.4f}", flush=True)
        model.eval()
        with torch.no_grad():
            val_total = 0.0
            for start in range(0, len(encoded_validation), args.batch_size):
                group = encoded_validation[start:start + args.batch_size]
                val_total += float(model(*collate(group, "cpu"))) * len(group)
        val_loss = val_total / len(encoded_validation)
        print(f"epoch {epoch + 1}: train {total/trained_rows:.4f}, real validation {val_loss:.4f}", flush=True)
        if val_loss < best_loss - .001:
            best_loss, stale = val_loss, 0
            torch.save(model.state_dict(), args.checkpoint)
        else:
            stale += 1
            if stale >= args.patience:
                print("early stopping on real validation loss", flush=True)
                break
    model.load_state_dict(torch.load(args.checkpoint, map_location="cpu", weights_only=True))
    args.out.parent.mkdir(parents=True, exist_ok=True)
    export_model(model, vocab, args.out)
    exported, exported_vocab = load_export(args.out)
    if not args.skip_eval:
        args.golden.parent.mkdir(parents=True, exist_ok=True)
        args.eval.parent.mkdir(parents=True, exist_ok=True)
        evaluate(exported, heldout, exported_vocab, args.eval, args.golden)
    print(f"artifact {args.out.stat().st_size} bytes, sha256 {hashlib.sha256(args.out.read_bytes()).hexdigest()}")


if __name__ == "__main__":
    main()
