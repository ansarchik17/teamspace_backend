import json
import tempfile
import unittest
from pathlib import Path

from evaluate_file_retrieval import read_cases, score_result


class EvaluationTests(unittest.TestCase):
    def test_rank_metrics_do_not_confuse_presence_with_top_match(self):
        result = score_result({"expected_file_ids": ["target"]}, {"sources": [{"file_id": "wrong"}, {"file_id": "target"}]})
        self.assertEqual(result["recall"], 1)
        self.assertEqual(result["mrr"], .5)
        self.assertFalse(result["top1"])

    def test_text_evidence_and_incomplete_index_are_reported(self):
        result = score_result({"expected_text": "HR-74291"}, {"sources": [{"file_id": "a", "chunks": [{"content": "Код HR-74291"}]}], "indexing": True})
        self.assertEqual(result["mrr"], 1)
        self.assertTrue(result["indexing"])

    def test_cases_need_ground_truth_before_network_calls(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "cases.jsonl"
            path.write_text(json.dumps({"id": "bad", "question": "How?"}), encoding="utf-8")
            with self.assertRaises(ValueError):
                read_cases(path)


if __name__ == "__main__":
    unittest.main()
