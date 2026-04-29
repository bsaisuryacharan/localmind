# sqlite-vec backend: design notes

Status: skeleton landed in `mcp/internal/store/sqlitevec.go`, behind the
`sqlitevec` build tag. No working implementation yet.

## Why sqlite-vec (not LanceDB or pgvector)

localmind's thesis is "self-hosted, single binary, no daemon." sqlite-vec
fits: it's a single-file database, no server process, embeds cleanly into
our Docker image, and the on-disk format is a plain `.sqlite` file users
can back up by copying. LanceDB pulls in a much heavier Arrow/columnar
dependency footprint; pgvector requires a Postgres service we don't want
to operate. sqlite-vec is the smallest delta from "JSON file on disk."

## CGO trade-off

sqlite-vec ships as a SQLite extension. Two paths:

- **Pure-Go (`modernc.org/sqlite`)**: no cgo, easy cross-compile, but
  extension loading is incomplete and `sqlite-vec` is not supported
  today.
- **CGO (`mattn/go-sqlite3` + `asg017/sqlite-vec-go-bindings`)**: full
  extension support, but every consumer needs a working C toolchain at
  build time. That breaks our "go install" story.

**Recommendation**: cgo path, gated behind a `sqlitevec` build tag.
Default builds stay cgo-free and ship `MemoryStore`. Power users (or our
own sqlite docker image) build with `-tags sqlitevec` and get the
backend. This keeps the cost out of the default user's path.

## Schema

```sql
CREATE TABLE IF NOT EXISTS docs(
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL,
  start INTEGER,
  end_   INTEGER,
  chunk TEXT,
  vec BLOB
);
CREATE VIRTUAL TABLE IF NOT EXISTS vec_idx USING vec0(
  embedding float[<DIM>]
);
CREATE INDEX IF NOT EXISTS docs_path_idx ON docs(path);
```

Search:

```sql
SELECT docs.path, docs.start, docs.end_, docs.chunk, distance
FROM   vec_idx
JOIN   docs ON docs.id = vec_idx.rowid
WHERE  vec_idx.embedding MATCH ?
ORDER  BY distance
LIMIT  ?;
```

`Replace(path, docs)` runs in a transaction: `DELETE FROM docs WHERE
path=?`, bulk `INSERT` the new chunks, then `INSERT` into `vec_idx` for
each new rowid.

## Migration plan

A user upgrading from a `MemoryStore`-only build to a `sqlitevec` build
already has an `index.json` on disk. On first `OpenSQLite(p)` call:

1. Look for `index.json` next to `p`.
2. If present and the sqlite file is empty, decode it and bulk-ingest
   the docs into the new schema.
3. Rename `index.json` → `index.json.migrated` so we don't re-run.

TODO: implement the migration helper alongside the real `OpenSQLite`.

## Cutover trigger

`localmind` should auto-pick the SQLite backend when `len(docs)` in the
existing `index.json` exceeds 50K (matches the 200 MB / cosine-loop
ceiling already documented in `store.go`). Below that, MemoryStore is
faster and has zero dependencies. Heuristic only — users can override
with `STORE_BACKEND=sqlite`.

## Build instructions for early testers

```
cd mcp && go build -tags sqlitevec ./...
```

Today this compiles (the skeleton has no third-party imports) but every
method panics. Once the impl lands, the same command will produce a
binary with a working SQLite backend — assuming a C toolchain is on
PATH.

## Future work checklist

- [ ] Implement `OpenSQLite`: open the file, load `sqlite-vec`, run the
      `CREATE TABLE` DDL.
- [ ] Implement `Replace` as a transaction.
- [ ] Implement `Search` using `vec_idx MATCH`.
- [ ] Implement `Remove`, `Paths`, `Has`, `Save`, `Close`.
- [ ] Wire `Open()` in `store.go` to dispatch on `*.sqlite` / `*.db`
      suffix when built with `-tags sqlitevec`.
- [ ] `index.json` → sqlite migration helper.
- [ ] Add `STORE_BACKEND=memory|sqlite` env var override.
- [ ] Add a `Dockerfile.sqlite` variant that includes sqlite-vec.
- [ ] Tests against a tmpdir-resident sqlite file (build-tagged).
