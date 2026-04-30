"""MCP tool gateway client + per-skill tool whitelist enforcement.

Every tool call from an orchestrator-spawned agent flows through this
module. We talk JSON-RPC 2.0 to the localmind MCP gateway (today
``http://mcp:7800/mcp`` inside the docker compose network) which
already exposes ``search_files``, ``list_files``, ``read_file`` over
the standard ``tools/list`` + ``tools/call`` methods.

The ``allowed`` parameter on :meth:`MCPTools.call` is the per-skill
whitelist: agents pass their skill's ``tools`` list. If the requested
tool is not in that list we raise :class:`ToolNotAllowedError` BEFORE
hitting the network. This is the default-deny posture documented in
the v0.3.0 plan.
"""

from __future__ import annotations

import os
from typing import Any

import httpx
from pydantic import BaseModel, Field


class ToolNotAllowedError(Exception):
    """Raised when an agent tries to use a tool not in its skill's whitelist."""


class ToolCallError(Exception):
    """Raised when the MCP gateway returns a JSON-RPC error."""


class ToolDescriptor(BaseModel):
    """A tool advertised by the MCP gateway via ``tools/list``."""

    name: str
    description: str
    input_schema: dict[str, Any] = Field(default_factory=dict)


def _default_base_url() -> str:
    return os.environ.get("LOCALMIND_MCP_URL", "http://mcp:7800/mcp")


def _default_token() -> str | None:
    tok = os.environ.get("LOCALMIND_MCP_TOKEN")
    return tok if tok else None


class MCPTools:
    """Async client for the localmind MCP gateway.

    Wraps JSON-RPC ``tools/list`` and ``tools/call``. Lifetime: the
    underlying ``httpx.AsyncClient`` is created lazily and closed via
    :meth:`aclose` (or the async-context-manager methods).

    Example:
        >>> tools = MCPTools()  # reads LOCALMIND_MCP_URL / _TOKEN from env
        >>> async with tools:
        ...     result = await tools.call(
        ...         "search_files",
        ...         {"query": "phoenix", "k": 3},
        ...         allowed=["search_files"],
        ...     )
    """

    def __init__(
        self,
        base_url: str | None = None,
        token: str | None = None,
        *,
        timeout: float = 30.0,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self.base_url = base_url if base_url is not None else _default_base_url()
        self.token = token if token is not None else _default_token()
        self._timeout = timeout
        self._client = client
        self._owned_client = client is None
        self._next_id = 0

    # --- lifecycle -------------------------------------------------

    async def __aenter__(self) -> MCPTools:
        self._ensure_client()
        return self

    async def __aexit__(self, *_exc: object) -> None:
        await self.aclose()

    def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(timeout=self._timeout)
            self._owned_client = True
        return self._client

    async def aclose(self) -> None:
        if self._client is not None and self._owned_client:
            await self._client.aclose()
            self._client = None

    # --- internals -------------------------------------------------

    def _headers(self) -> dict[str, str]:
        h = {"Content-Type": "application/json", "Accept": "application/json"}
        if self.token:
            h["Authorization"] = f"Bearer {self.token}"
        return h

    def _rpc_id(self) -> int:
        self._next_id += 1
        return self._next_id

    async def _rpc(self, method: str, params: dict[str, Any]) -> Any:
        client = self._ensure_client()
        payload = {
            "jsonrpc": "2.0",
            "id": self._rpc_id(),
            "method": method,
            "params": params,
        }
        resp = await client.post(
            self.base_url, json=payload, headers=self._headers()
        )
        resp.raise_for_status()
        body = resp.json()
        if "error" in body and body["error"]:
            err = body["error"]
            raise ToolCallError(
                f"MCP error {err.get('code')}: {err.get('message')}"
            )
        return body.get("result")

    @staticmethod
    def _extract_text(result: Any) -> str:
        """Pull the text payload out of an MCP tools/call response.

        MCP responses look like ``{"content": [{"type": "text", "text": "..."}, ...]}``.
        We concatenate every ``text``-typed part. Non-text parts are
        rendered as a short tag so callers don't silently lose data.
        """
        if result is None:
            return ""
        if isinstance(result, str):
            return result
        if isinstance(result, dict):
            content = result.get("content")
            if isinstance(content, list):
                parts: list[str] = []
                for item in content:
                    if not isinstance(item, dict):
                        continue
                    kind = item.get("type")
                    if kind == "text":
                        parts.append(str(item.get("text", "")))
                    else:
                        parts.append(f"[{kind or 'unknown'} content omitted]")
                return "".join(parts)
            # Fallback: server returned a bare result object.
            if "text" in result:
                return str(result["text"])
        return str(result)

    # --- public API ------------------------------------------------

    async def list(self) -> list[ToolDescriptor]:
        """JSON-RPC ``tools/list`` -- enumerate available tools."""
        result = await self._rpc("tools/list", {})
        tools_raw = (result or {}).get("tools") or []
        out: list[ToolDescriptor] = []
        for t in tools_raw:
            if not isinstance(t, dict):
                continue
            out.append(
                ToolDescriptor(
                    name=str(t.get("name", "")),
                    description=str(t.get("description", "")),
                    input_schema=dict(
                        t.get("inputSchema") or t.get("input_schema") or {}
                    ),
                )
            )
        return out

    async def call(
        self,
        name: str,
        arguments: dict[str, Any],
        *,
        allowed: list[str],
    ) -> str:
        """JSON-RPC ``tools/call`` with per-skill whitelist enforcement.

        Args:
            name: tool name (e.g. ``search_files``).
            arguments: JSON-serializable args for the tool.
            allowed: the calling skill's tool whitelist. Required --
                this is the default-deny gate. Pass an empty list to
                forbid all tool calls for this agent.

        Raises:
            ToolNotAllowedError: if ``name`` is not in ``allowed``.
            ToolCallError: if the MCP gateway returns a JSON-RPC error.
            httpx.HTTPError: for transport-level failures.

        Returns:
            The concatenated text content of the MCP response.
        """
        if name not in allowed:
            raise ToolNotAllowedError(
                f"tool {name!r} is not in this skill's whitelist "
                f"(allowed: {allowed})"
            )
        result = await self._rpc(
            "tools/call", {"name": name, "arguments": arguments}
        )
        return self._extract_text(result)


__all__ = [
    "MCPTools",
    "ToolDescriptor",
    "ToolNotAllowedError",
    "ToolCallError",
]
