#!/usr/bin/env python3
"""Read-only retrieval evaluation against one explicitly selected backend.

Run twice for a baseline/candidate comparison. Does not ask an LLM to grade itself
and never prints credentials, query text, or retrieved private excerpts.
"""
from __future__ import annotations

import argparse
import json
import os
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any
from uuid import UUID


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def score_result(case: dict[str, Any], response: dict[str, Any]) -> dict[str, Any]:
    sources = response.get("sources")
    if not isinstance(sources, list):
        raise ValueError("Invalid retrieval response")
    expected_ids = set(case.get("expected_file_ids", []))
    expected_text = case.get("expected_text", "").casefold()
    ranks: list[int] = []
    retrieved_ids = set()
    for rank, source in enumerate(sources, 1):
        if not isinstance(source, dict):
            raise ValueError("Invalid retrieval source")
        retrieved_ids.add(source.get("file_id"))
        content = "\n".join(chunk.get("content", "") for chunk in source.get("chunks", []))
        if source.get("file_id") in expected_ids or (expected_text and expected_text in content.casefold()):
            ranks.append(rank)
    recall = len(expected_ids & retrieved_ids) / len(expected_ids) if expected_ids else float(bool(ranks))
    return {"recall": recall, "mrr": 1 / min(ranks) if ranks else 0.0, "top1": bool(ranks and min(ranks) == 1), "indexing": bool(response.get("indexing") or response.get("incomplete")), "sources": len(sources)}


def read_cases(path: Path) -> list[dict[str, Any]]:
    cases = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict) or not isinstance(value.get("id"), str) or not isinstance(value.get("question"), str):
            raise ValueError("Every case needs id and question")
        if not value["question"].strip() or len(value["question"]) > 2000 or len(value["id"]) > 128:
            raise ValueError("Invalid case size")
        if not value.get("expected_file_ids") and not value.get("expected_text"):
            raise ValueError("Every case needs expected_file_ids or expected_text")
        if not isinstance(value.get("file_ids", []), list) or len(value.get("file_ids", [])) > 32:
            raise ValueError("Invalid selected files")
        if not isinstance(value.get("expected_file_ids", []), list) or not isinstance(value.get("expected_text", ""), str):
            raise ValueError("Invalid expected evidence")
        for key in ("file_ids", "expected_file_ids"):
            values = value.get(key, [])
            if any(not isinstance(item, str) for item in values):
                raise ValueError("File IDs must be strings")
            for item in values:
                UUID(item)
        cases.append(value)
        if len(cases) > 1000:
            raise ValueError("At most 1000 cases per run")
    if not cases:
        raise ValueError("No cases")
    return cases


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True, help="Backend base URL including /api/v1")
    parser.add_argument("--cases", required=True, type=Path, help="UTF-8 JSONL cases")
    parser.add_argument("--label", default="candidate")
    parser.add_argument("--token-env", default="RETRIEVAL_EVAL_TOKEN")
    parser.add_argument("--limit", type=int, default=8, choices=range(1, 21))
    args = parser.parse_args()
    url = urllib.parse.urlsplit(args.url)
    if url.scheme not in {"http", "https"} or not url.netloc or url.username or url.query or url.fragment:
        parser.error("Use an HTTP(S) base URL without credentials or query parameters")
    token = os.environ.get(args.token_env, "").strip()
    if not token:
        parser.error(f"Set {args.token_env} in the operator environment")
    cases = read_cases(args.cases)  # Validate the entire suite before any request.
    opener = urllib.request.build_opener(NoRedirect())
    results = []
    for case in cases:
        request = urllib.request.Request(
            args.url.rstrip("/") + "/drive/search/query",
            data=json.dumps({"question": case["question"], "file_ids": case.get("file_ids", []), "limit": args.limit, "chunks_per_file": 4}).encode("utf-8"),
            headers={"Authorization": token if token.startswith("Bearer ") else "Bearer " + token, "Content-Type": "application/json"},
            method="POST",
        )
        started = time.monotonic()
        try:
            with opener.open(request, timeout=45) as response:
                raw = response.read(2 * 1024 * 1024 + 1)
                if len(raw) > 2 * 1024 * 1024:
                    raise ValueError("Retrieval response exceeds limit")
                result = score_result(case, json.loads(raw))
        except urllib.error.HTTPError as exc:
            # No retries: old search pipelines may charge for model calls.
            raise SystemExit(f"Case {case['id']}: HTTP {exc.code}") from None
        except (urllib.error.URLError, ValueError, TypeError, KeyError):
            raise SystemExit(f"Case {case['id']}: retrieval evaluation failed") from None
        result.update({"case": case["id"], "duration_ms": round((time.monotonic() - started) * 1000, 2)})
        results.append(result)
        print(json.dumps(result, ensure_ascii=False))
    durations = sorted(item["duration_ms"] for item in results)
    print(json.dumps({"summary": args.label, "cases": len(results), "recall": statistics.mean(item["recall"] for item in results), "mrr": statistics.mean(item["mrr"] for item in results), "top1": statistics.mean(item["top1"] for item in results), "p95_ms": durations[max(0, int(len(durations) * .95) - 1)], "incomplete": sum(item["indexing"] for item in results)}, ensure_ascii=False))
    return 2 if any(item["indexing"] for item in results) else 0


if __name__ == "__main__":
    raise SystemExit(main())
