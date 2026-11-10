import threading
import unittest

from server import BusyError, RankingEngine, validate_request


class FakeModel:
    def predict(self, pairs, **kwargs):
        return [0.9 if "договор" in text else 0.1 for _, text in pairs]


class RankingTests(unittest.TestCase):
    def test_scores_preserve_input_indices(self):
        result = RankingEngine(FakeModel()).rank({"query": "Как уйти?", "documents": ["Погода", "Трудовой договор"]})
        self.assertEqual(result["results"], [{"index": 0, "relevance_score": 0.1}, {"index": 1, "relevance_score": 0.9}])

    def test_rejects_unbounded_or_malformed_requests(self):
        for payload in [{}, {"query": "q", "documents": ["x"] * 81}, {"query": "q", "documents": ["x" * 6001]}, {"query": "q", "documents": []}]:
            with self.subTest(payload_type=type(payload)):
                with self.assertRaises(ValueError):
                    validate_request(payload)

    def test_busy_model_does_not_queue_unbounded_work(self):
        engine = RankingEngine(FakeModel())
        engine.lock.acquire()
        try:
            with self.assertRaises(BusyError):
                engine.rank({"query": "q", "documents": ["x"]})
        finally:
            engine.lock.release()

    def test_invalid_model_result_releases_capacity(self):
        model = FakeModel()
        model.predict = lambda *args, **kwargs: [float("nan")]
        engine = RankingEngine(model)
        with self.assertRaises(ValueError):
            engine.rank({"query": "q", "documents": ["x"]})
        self.assertTrue(engine.lock.acquire(blocking=False))
        engine.lock.release()


if __name__ == "__main__":
    unittest.main()
