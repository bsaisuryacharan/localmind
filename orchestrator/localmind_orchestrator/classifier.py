"""Complexity classifier.

Asks the cheapest local model to label a query as one of
``direct`` / ``light`` / ``full``. If the LLM mis-labels (or is offline),
falls back to a verb-counting heuristic so we always return *something*.

The classifier is deliberately fast: ~50 token generation, single shot,
no streaming. The orchestrator gates the rest of the run on this.
"""
from __future__ import annotations

import logging
import re
from typing import Literal

from .ollama import OllamaClient

logger = logging.getLogger(__name__)

Mode = Literal["direct", "light", "full"]

_VALID_LABELS: set[Mode] = {"direct", "light", "full"}

CLASSIFIER_SYSTEM = """You are a routing classifier. Read the user's query and reply with EXACTLY one word — one of: direct, light, full.

Definitions:
- direct: greetings, factual one-liners, conversational acknowledgements, anything answerable in fewer than 5 sentences from general knowledge alone, and that needs no tools.
  Examples: "hi", "what's 2+2", "thanks", "what's the capital of France", "explain recursion in one sentence".
- light: a single-action query that needs exactly one tool call (search, read a file, list things).
  Examples: "search my notes for kubernetes", "read README.md", "list PDFs in /docs", "what does my last meeting transcript say about pricing".
- full: anything multi-step, multi-domain, or that benefits from parallel work.
  Examples: "summarise every PDF in this folder and propose follow-up questions", "review my whole repo and suggest fixes", "for each transcript, extract action items and group by owner".

Reply with ONLY the single label, lowercase, no punctuation, no explanation."""


# Verbs we count for the heuristic fallback. Doesn't have to be exhaustive —
# just enough signal to pick between direct/light/full when the LLM is unreachable.
_HEURISTIC_VERBS = re.compile(
    r"\b(summari[sz]e|review|analy[sz]e|compare|search|read|list|find|extract|propose|suggest|generate|write|draft|build|run|check|fix|refactor|investigate|cluster|group|rank|score)\b",
    re.IGNORECASE,
)
_HEURISTIC_FANOUT = re.compile(
    r"\b(and then|for each|every|all of (?:my|the)|across (?:my|the)|each of)\b",
    re.IGNORECASE,
)
_HEURISTIC_LIGHT_TOOL = re.compile(
    r"\b(search|read|list|find|open|show)\b",
    re.IGNORECASE,
)


def _heuristic(query: str) -> Mode:
    fanout = len(_HEURISTIC_FANOUT.findall(query))
    verbs = len(set(m.group(0).lower() for m in _HEURISTIC_VERBS.finditer(query)))
    score = fanout + max(0, verbs - 1)
    if score >= 2:
        return "full"
    if _HEURISTIC_LIGHT_TOOL.search(query) or verbs >= 1:
        return "light"
    return "direct"


def _parse_label(text: str) -> Mode | None:
    """Pick the first valid label out of the model's reply."""
    if not text:
        return None
    # Be permissive: the model may wrap it in quotes, prefix with "label:", etc.
    m = re.search(r"\b(direct|light|full)\b", text, re.IGNORECASE)
    if not m:
        return None
    label = m.group(1).lower()
    return label if label in _VALID_LABELS else None  # type: ignore[return-value]


async def classify(
    query: str,
    ollama: OllamaClient,
    model: str = "qwen2.5:3b",
) -> Mode:
    """Decide between direct / light / full for ``query``.

    Falls back to the heuristic if the LLM call fails or returns garbage.
    """
    query = query.strip()
    if not query:
        return "direct"
    try:
        text = await ollama.generate(
            model=model,
            prompt=f"User query: {query!r}\nLabel:",
            system=CLASSIFIER_SYSTEM,
            max_tokens=8,
        )
    except Exception as exc:  # network, model missing, etc.
        logger.warning("classifier LLM unavailable, using heuristic: %s", exc)
        return _heuristic(query)
    label = _parse_label(text)
    if label is None:
        logger.info("classifier returned unparsable %r, using heuristic", text)
        return _heuristic(query)
    return label
