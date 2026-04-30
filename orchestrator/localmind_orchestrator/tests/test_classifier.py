"""Smoke tests for the complexity classifier.

We mock the OllamaClient so the tests run without a live model.
"""
from __future__ import annotations

import pytest

from localmind_orchestrator.classifier import classify, _heuristic


class _FakeOllama:
    def __init__(self, label: str) -> None:
        self._label = label

    async def generate(self, *, model: str, prompt: str, system: str | None = None,
                       max_tokens: int = 512) -> str:
        return self._label


@pytest.mark.asyncio
async def test_classifier_routes_direct() -> None:
    ollama = _FakeOllama("direct")
    out = await classify("hi there", ollama)  # type: ignore[arg-type]
    assert out == "direct"


@pytest.mark.asyncio
async def test_classifier_routes_light() -> None:
    ollama = _FakeOllama("light")
    out = await classify("search my notes for kubernetes", ollama)  # type: ignore[arg-type]
    assert out == "light"


@pytest.mark.asyncio
async def test_classifier_routes_full() -> None:
    ollama = _FakeOllama("full")
    out = await classify(
        "summarize every PDF in the folder and propose follow-up questions for each",
        ollama,  # type: ignore[arg-type]
    )
    assert out == "full"


@pytest.mark.asyncio
async def test_classifier_falls_back_to_heuristic_on_garbage() -> None:
    ollama = _FakeOllama("¯\\_(ツ)_/¯")
    out = await classify("search my notes for kubernetes", ollama)  # type: ignore[arg-type]
    # Heuristic should classify as light (single search verb).
    assert out in {"light", "full"}


@pytest.mark.asyncio
async def test_classifier_falls_back_when_llm_raises() -> None:
    class _BoomOllama:
        async def generate(self, **_kwargs: object) -> str:
            raise RuntimeError("ollama down")

    out = await classify("hi", _BoomOllama())  # type: ignore[arg-type]
    # "hi" hits no verbs, no fanout, no tool words → direct.
    assert out == "direct"


def test_heuristic_buckets() -> None:
    assert _heuristic("hi") == "direct"
    assert _heuristic("read README.md") == "light"
    assert _heuristic("summarize every PDF and review each transcript") == "full"
