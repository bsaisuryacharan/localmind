# MCP gateway

`localmind-mcp` is a small HTTP server that turns the contents of `./data/` into three MCP tools:

| Tool          | Args                          | Returns                                  |
| ------------- | ----------------------------- | ---------------------------------------- |
| `search_files`| `query: string, k: int=8`     | top-k chunks ranked by cosine similarity |
| `list_files`  | none                          | every indexed file path                  |
| `read_file`   | `path: string`                | the file's full contents                 |

## Connect from Claude Code

```bash
claude mcp add localmind http://localhost:7800/mcp
```

Then in any Claude session: *"search my notes for last week's meeting decisions"* — Claude tool-calls into the gateway and you get results from your local index.

## What gets indexed

- File extensions: `.md`, `.markdown`, `.txt`, `.rst`
- Files larger than 4 MB are skipped (configurable via `MaxFileBytes`)
- Files with NUL bytes in the first 512 bytes are treated as binary and skipped
- An fsnotify watcher picks up new, modified, and deleted files in near real time. Editor-style multiple-write events are debounced to a single ingest 500 ms after the last write. A safety rescan still runs every 30 s to catch anything fsnotify missed (overflow, container start races). On filesystems where fsnotify cannot be initialized, the indexer falls back to the safety rescan alone.

## How it works

1. **Chunker** splits each file into ~1500-byte windows with 150-byte overlap, preferring newline boundaries.
2. **Embedder** sends each chunk to Ollama's `/api/embeddings` (default model: `nomic-embed-text`).
3. **Store** keeps `(path, byte range, chunk, vector)` rows in memory and persists them to `INDEX_DIR/index.json` so warm restarts don't re-embed.
4. **Search** embeds the query with the same model, computes cosine similarity against every chunk, and returns the top-k.

## Configuration

| Env var           | Default                  | Purpose                                |
| ----------------- | ------------------------ | -------------------------------------- |
| `LOCALMIND_ADDR`  | `:7800`                  | listen address                         |
| `DATA_DIR`        | `/data`                  | watched folder (read-only mount)       |
| `INDEX_DIR`       | `/var/lib/localmind`     | persistence target for `index.json`    |
| `EMBEDDING_MODEL` | `nomic-embed-text`       | Ollama model used for embeddings       |
| `OLLAMA_BASE_URL` | `http://ollama:11434`    | Ollama HTTP endpoint                   |

## Limits and known gaps

- In-memory store; ~50K chunks fit in ~200 MB. Beyond that, swap in sqlite-vec.
- No PDF / DOCX support yet. Drop converted Markdown into `data/` as a workaround.
- Single embedding model per server. Switching models invalidates the index.
