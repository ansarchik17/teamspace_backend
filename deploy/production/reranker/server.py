"""Self-hosted cross-encoder; CPU-only, bounded requests, no hosted AI API."""
from __future__ import annotations

import json
import math
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


class BusyError(Exception):
    pass


def validate_request(payload: Any) -> tuple[str, list[str]]:
    if not isinstance(payload, dict) or set(payload) != {"query", "documents"}:
        raise ValueError("Expected query and documents")
    query, documents = payload["query"], payload["documents"]
    if not isinstance(query, str) or not query.strip() or len(query) > 2000:
        raise ValueError("Invalid query")
    if not isinstance(documents, list) or not 1 <= len(documents) <= 80:
        raise ValueError("Invalid candidate count")
    if any(not isinstance(text, str) or not text.strip() or len(text) > 6000 for text in documents):
        raise ValueError("Invalid candidate text")
    return query, documents


class RankingEngine:
    def __init__(self, model: Any):
        self.model = model
        self.lock = threading.Lock()

    def rank(self, payload: Any) -> dict[str, Any]:
        query, documents = validate_request(payload)
        if not self.lock.acquire(blocking=False):
            raise BusyError("Reranker is busy")
        try:
            raw = self.model.predict([(query, text) for text in documents], batch_size=4, show_progress_bar=False)
            scores = [float(score) for score in raw]
            if len(scores) != len(documents) or any(not math.isfinite(score) for score in scores):
                raise ValueError("Invalid model output")
            return {"results": [{"index": i, "relevance_score": score} for i, score in enumerate(scores)]}
        finally:
            self.lock.release()


def run() -> None:
    # All weights must be provisioned explicitly before startup. Loading models
    # never executes code supplied by a model repository or downloads on demand.
    path = os.environ.get("DOCUMENT_RERANK_MODEL_PATH", "/models/model")
    if not os.path.isdir(path):
        raise RuntimeError("Provision the local reranker model directory first")
    import torch
    from sentence_transformers import CrossEncoder

    torch.set_num_threads(2)
    model = CrossEncoder(path, device="cpu", max_length=1024, trust_remote_code=False, local_files_only=True)
    engine = RankingEngine(model)

    class Handler(BaseHTTPRequestHandler):
        def send_json(self, status: int, value: dict[str, Any]) -> None:
            data = json.dumps(value, allow_nan=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            try:
                self.wfile.write(data)
            except (BrokenPipeError, ConnectionResetError):
                pass

        def do_GET(self) -> None:
            self.send_json(200 if self.path == "/health" else 404, {"ready": self.path == "/health"})

        def do_POST(self) -> None:
            if self.path != "/rerank":
                self.send_json(404, {"error": "Not found"})
                return
            try:
                self.connection.settimeout(10)
                size = int(self.headers.get("Content-Length", "0"))
                if not 0 < size <= 2 * 1024 * 1024:
                    self.send_json(413, {"error": "Request exceeds limit"})
                    return
                payload = json.loads(self.rfile.read(size))
                result = engine.rank(payload)
            except BusyError:
                self.send_json(429, {"error": "Reranker is busy"})
                return
            except (ValueError, TypeError, TimeoutError):
                self.send_json(422, {"error": "Invalid ranking request or model result"})
                return
            except Exception:
                self.send_json(503, {"error": "Reranker unavailable"})
                return
            self.send_json(200, result)

        def log_message(self, format: str, *args: Any) -> None:
            # Do not log private query strings, candidate text, or request bodies.
            pass

    server = ThreadingHTTPServer(("0.0.0.0", 8008), Handler)
    server.daemon_threads = True
    server.serve_forever()


if __name__ == "__main__":
    run()
