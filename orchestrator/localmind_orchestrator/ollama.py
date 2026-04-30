"""Thin async HTTP client for Ollama's /api/chat and /api/generate.

We deliberately keep this minimal: the orchestrator only needs LLM calls.
Embeddings and tool-use parsing live elsewhere (MCP gateway / skill prompts).
"""
from __future__ import annotations

import json
import logging
import os
from typing import AsyncIterator

import httpx

logger = logging.getLogger(__name__)

DEFAULT_BASE_URL = os.environ.get("OLLAMA_BASE_URL", "http://ollama:11434")


class OllamaClient:
    """Async client for Ollama. One instance per process is fine; httpx pools internally."""

    def __init__(self, base_url: str | None = None, *, timeout: float = 120.0) -> None:
        self.base_url = (base_url or DEFAULT_BASE_URL).rstrip("/")
        self._timeout = timeout
        # Use a lazy client so tests can monkeypatch without spinning a real connection pool.
        self._client: httpx.AsyncClient | None = None

    async def _get_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(base_url=self.base_url, timeout=self._timeout)
        return self._client

    async def aclose(self) -> None:
        if self._client is not None:
            await self._client.aclose()
            self._client = None

    async def generate(
        self,
        model: str,
        prompt: str,
        *,
        max_tokens: int = 512,
        system: str | None = None,
    ) -> str:
        """Non-streaming completion. Returns the full response text.

        Used by the classifier and any other call site that wants a single short answer.
        """
        payload: dict = {
            "model": model,
            "prompt": prompt,
            "stream": False,
            "options": {"num_predict": max_tokens},
        }
        if system is not None:
            payload["system"] = system
        client = await self._get_client()
        try:
            resp = await client.post("/api/generate", json=payload)
            resp.raise_for_status()
            data = resp.json()
            return str(data.get("response", "")).strip()
        except httpx.HTTPError as exc:
            logger.warning("ollama.generate failed: %s", exc)
            raise

    async def chat(
        self,
        model: str,
        messages: list[dict],
        *,
        max_tokens: int = 1024,
        system: str | None = None,
    ) -> AsyncIterator[str]:
        """Streaming chat completion. Yields token chunks (str) as they arrive.

        `messages` is a list of {role, content} dicts, OpenAI-style.
        If `system` is given, it is prepended as the first system message
        unless the caller already supplied one.
        """
        msgs = list(messages)
        if system is not None and not (msgs and msgs[0].get("role") == "system"):
            msgs = [{"role": "system", "content": system}, *msgs]

        payload = {
            "model": model,
            "messages": msgs,
            "stream": True,
            "options": {"num_predict": max_tokens},
        }
        client = await self._get_client()
        async with client.stream("POST", "/api/chat", json=payload) as resp:
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                if not line:
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    logger.debug("ollama.chat: skipping non-JSON line: %r", line[:80])
                    continue
                if obj.get("done"):
                    break
                msg = obj.get("message") or {}
                chunk = msg.get("content")
                if chunk:
                    yield chunk
