"""Tests for the MCPTools wrapper + whitelist enforcement."""

from __future__ import annotations

import json
from typing import Any

import httpx
import pytest

from localmind_orchestrator.tools import (
    MCPTools,
    ToolCallError,
    ToolDescriptor,
    ToolNotAllowedError,
)


def _make_client(handler: Any) -> httpx.AsyncClient:
    """Build an httpx.AsyncClient backed by a MockTransport handler."""
    transport = httpx.MockTransport(handler)
    return httpx.AsyncClient(transport=transport)


@pytest.mark.asyncio
async def test_call_issues_correct_jsonrpc_and_returns_text() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["headers"] = dict(request.headers)
        captured["body"] = json.loads(request.content.decode("utf-8"))
        return httpx.Response(
            200,
            json={
                "jsonrpc": "2.0",
                "id": captured["body"]["id"],
                "result": {
                    "content": [
                        {"type": "text", "text": "hit-1\n"},
                        {"type": "text", "text": "hit-2"},
                    ]
                },
            },
        )

    async with MCPTools(
        base_url="http://mcp:7800/mcp",
        token="secret",
        client=_make_client(handler),
    ) as tools:
        out = await tools.call(
            "search_files",
            {"query": "phoenix", "k": 3},
            allowed=["search_files", "list_files"],
        )

    assert out == "hit-1\nhit-2"

    body = captured["body"]
    assert body["jsonrpc"] == "2.0"
    assert body["method"] == "tools/call"
    assert body["params"] == {
        "name": "search_files",
        "arguments": {"query": "phoenix", "k": 3},
    }
    assert isinstance(body["id"], int)
    assert captured["url"] == "http://mcp:7800/mcp"
    assert captured["headers"].get("authorization") == "Bearer secret"


@pytest.mark.asyncio
async def test_call_disallowed_tool_raises_before_network() -> None:
    """A tool not in `allowed` must raise without ever hitting the wire."""
    called = False

    def handler(_request: httpx.Request) -> httpx.Response:
        nonlocal called
        called = True
        return httpx.Response(200, json={"jsonrpc": "2.0", "id": 1, "result": {}})

    async with MCPTools(
        base_url="http://mcp:7800/mcp",
        client=_make_client(handler),
    ) as tools:
        with pytest.raises(ToolNotAllowedError):
            await tools.call("write_file", {}, allowed=["search_files"])

    assert called is False, "request should never reach the transport"


@pytest.mark.asyncio
async def test_call_with_empty_allowed_list_denies_everything() -> None:
    async with MCPTools(client=_make_client(lambda r: httpx.Response(200))) as tools:
        with pytest.raises(ToolNotAllowedError):
            await tools.call("search_files", {}, allowed=[])


@pytest.mark.asyncio
async def test_no_token_omits_authorization_header() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["headers"] = dict(request.headers)
        return httpx.Response(
            200,
            json={"jsonrpc": "2.0", "id": 1, "result": {"content": []}},
        )

    async with MCPTools(
        base_url="http://mcp:7800/mcp",
        token=None,
        client=_make_client(handler),
    ) as tools:
        await tools.call("search_files", {"q": "x"}, allowed=["search_files"])

    assert "authorization" not in captured["headers"]


@pytest.mark.asyncio
async def test_jsonrpc_error_raises_toolcallerror() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "jsonrpc": "2.0",
                "id": 1,
                "error": {"code": -32601, "message": "method not found"},
            },
        )

    async with MCPTools(client=_make_client(handler)) as tools:
        with pytest.raises(ToolCallError, match="method not found"):
            await tools.call("search_files", {}, allowed=["search_files"])


@pytest.mark.asyncio
async def test_list_returns_tool_descriptors() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "jsonrpc": "2.0",
                "id": 1,
                "result": {
                    "tools": [
                        {
                            "name": "search_files",
                            "description": "semantic search",
                            "inputSchema": {"type": "object"},
                        },
                        {
                            "name": "read_file",
                            "description": "read a file",
                            "inputSchema": {"type": "object"},
                        },
                    ]
                },
            },
        )

    async with MCPTools(client=_make_client(handler)) as tools:
        descriptors = await tools.list()

    assert len(descriptors) == 2
    assert all(isinstance(d, ToolDescriptor) for d in descriptors)
    assert {d.name for d in descriptors} == {"search_files", "read_file"}
    assert descriptors[0].input_schema == {"type": "object"}


@pytest.mark.asyncio
async def test_extract_text_handles_non_text_parts() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "jsonrpc": "2.0",
                "id": 1,
                "result": {
                    "content": [
                        {"type": "text", "text": "before "},
                        {"type": "image", "data": "..."},
                        {"type": "text", "text": " after"},
                    ]
                },
            },
        )

    async with MCPTools(client=_make_client(handler)) as tools:
        out = await tools.call(
            "read_file", {"path": "x"}, allowed=["read_file"]
        )

    assert out == "before [image content omitted] after"


@pytest.mark.asyncio
async def test_env_defaults(monkeypatch: pytest.MonkeyPatch) -> None:
    """Constructor defaults pull from LOCALMIND_MCP_URL / _TOKEN."""
    monkeypatch.setenv("LOCALMIND_MCP_URL", "http://example:9000/mcp")
    monkeypatch.setenv("LOCALMIND_MCP_TOKEN", "tok-from-env")
    tools = MCPTools()
    assert tools.base_url == "http://example:9000/mcp"
    assert tools.token == "tok-from-env"
    await tools.aclose()


@pytest.mark.asyncio
async def test_env_token_empty_treated_as_none(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.delenv("LOCALMIND_MCP_URL", raising=False)
    monkeypatch.setenv("LOCALMIND_MCP_TOKEN", "")
    tools = MCPTools()
    assert tools.token is None
    await tools.aclose()
