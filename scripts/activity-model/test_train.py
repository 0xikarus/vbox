import unittest
import json
import tempfile
from pathlib import Path

import train


class ExtraRealLeakageTest(unittest.TestCase):
    def sample(self, identifier, tail):
        return train.Sample(identifier, "assistant: " + tail, "Reviewing branch changes", "real")

    def test_near_duplicate_heldout_tail_is_removed(self):
        detail = "Reviewing the controller branch and its mobile screenshot results before the merge. " * 3
        heldout = self.sample("heldout", detail)
        extra = self.sample("extra", "Earlier context. " + detail)
        kept, removed = train.filter_extra_real([extra], [heldout], {heldout.id})
        self.assertEqual(kept, [])
        self.assertEqual(removed.get("protected_tail_overlap"), 1)

    def test_distinct_prose_and_duplicate_ids(self):
        heldout = self.sample("heldout", "Checking the mobile gesture tests before sending the screenshots to the owner.")
        unique = self.sample("unique", "Writing a new database migration for the account settings service.")
        duplicate_id = self.sample("heldout", "Completely different text but the heldout identifier is reused.")
        kept, removed = train.filter_extra_real([unique, duplicate_id], [heldout], {heldout.id})
        self.assertEqual([item.id for item in kept], ["unique"])
        self.assertEqual(removed.get("existing_id"), 1)

    def test_same_model_input_with_different_punctuation_is_removed(self):
        words = "reviewing screenshots and checking builder results before sending fixes to the owner " * 4
        heldout = self.sample("heldout", words)
        extra = self.sample("extra", words.replace(" ", ", "))
        kept, removed = train.filter_extra_real([extra], [heldout], set())
        self.assertEqual(kept, [])
        self.assertEqual(removed.get("protected_input_overlap"), 1)

    def test_extra_verb_balance_is_deterministic(self):
        rows = [train.Sample(str(index), "A unique input " + str(index), "Reporting progress", "extra_real")
                for index in range(5)]
        rows.append(train.Sample("other", "Different input", "Reviewing screenshots", "extra_real"))
        selected, removed = train.balance_extra_real(rows, 2)
        self.assertEqual(removed, 3)
        self.assertEqual(sum(item.phrase.startswith("Reporting") for item in selected), 2)
        self.assertEqual(sum(item.phrase.startswith("Reviewing") for item in selected), 1)
        self.assertEqual(train.balance_extra_real(list(reversed(rows)), 2)[0], selected)

    def test_near_duplicate_extra_windows_are_removed(self):
        action = "I am reviewing the controller screenshots before sending the fixes to the builder. " * 4
        first = self.sample("a", action)
        second = self.sample("b", "A different earlier line. " + action)
        kept, removed = train.filter_extra_real([first, second], [], set())
        self.assertEqual(len(kept), 1)
        self.assertEqual(removed.get("duplicate_extra_tail"), 1)

    def test_synthetic_split_keeps_duplicate_text_out_of_holdout(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "synthetic.jsonl"
            rows = [{"id": f"example-{index}", "text": f"assistant: Checking synthetic task {index}", "phrase": "Checking task"}
                    for index in range(40)]
            rows.append({"id": "duplicate-id", "text": rows[0]["text"], "phrase": rows[0]["phrase"]})
            path.write_text("".join(json.dumps(row) + "\n" for row in rows))
            training, validation, heldout = train.load_data(path)
            self.assertEqual(len(training) + len(validation) + len(heldout), 40)
            self.assertFalse({row.text for row in training} & {row.text for row in heldout})
            self.assertFalse({row.text for row in training} & {row.text for row in validation})


if __name__ == "__main__":
    unittest.main()
