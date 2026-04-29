//go:build sqlitevec

// Package store: SQLite + sqlite-vec backend.
//
// Build with: go build -tags sqlitevec ./...
//
// This file is a SKELETON. The actual SQLite + sqlite-vec wiring is
// deferred to a follow-up. The build tag `sqlitevec` keeps it out of
// the default build so we don't take the cgo dependency until we're
// ready to ship it.
package store

import "errors"

// SQLiteStore will implement the Store interface backed by SQLite with
// the sqlite-vec extension loaded.
//
// Open path:
//
//  1. open the sqlite file (modernc.org/sqlite or mattn/go-sqlite3)
//  2. load the sqlite-vec extension (sqlite_load_extension or static link)
//  3. CREATE TABLE IF NOT EXISTS docs(
//       id INTEGER PRIMARY KEY,
//       path TEXT NOT NULL,
//       start INTEGER,
//       end_   INTEGER,
//       chunk TEXT,
//       vec BLOB
//     );
//     CREATE VIRTUAL TABLE IF NOT EXISTS vec_idx USING vec0(
//       embedding float[<DIM>]
//     );
//     CREATE INDEX IF NOT EXISTS docs_path_idx ON docs(path);
//
// Search becomes:
//
//	SELECT docs.path, docs.start, docs.end_, docs.chunk,
//	       distance
//	FROM   vec_idx
//	JOIN   docs ON docs.id = vec_idx.rowid
//	WHERE  vec_idx.embedding MATCH ?
//	ORDER  BY distance
//	LIMIT  ?
//
// Replace becomes a transaction: DELETE FROM docs WHERE path=?,
// then bulk INSERT, then INSERT into vec_idx for each new row.

// errSQLiteVecNotBuilt is returned by OpenSQLite when the skeleton
// file is reached. Even with `-tags sqlitevec`, the impl is empty,
// so this sentinel exists to give a developer who wires it up too
// early a clear, actionable error.
var errSQLiteVecNotBuilt = errors.New("store: sqlite-vec backend not compiled in (build with -tags sqlitevec); the impl itself is also skeleton-only — see TODO in sqlitevec.go")

// SQLiteStore is the future SQLite-backed Store implementation.
// It is intentionally unimplemented today; calling any method panics
// with a clear message so a developer who forgot to remove the build
// tag gets a fast signal.
type SQLiteStore struct {
	// path to the sqlite file
	path string
}

// OpenSQLite is the constructor a future Open() switch will dispatch to
// when persistPath has a `.sqlite` or `.db` extension.
func OpenSQLite(path string) (*SQLiteStore, error) {
	return nil, errSQLiteVecNotBuilt
}

// Replace is unimplemented; see package doc.
func (s *SQLiteStore) Replace(path string, docs []Doc) {
	panic("SQLiteStore: not implemented")
}

// Remove is unimplemented; see package doc.
func (s *SQLiteStore) Remove(path string) {
	panic("SQLiteStore: not implemented")
}

// Paths is unimplemented; see package doc.
func (s *SQLiteStore) Paths() []string {
	panic("SQLiteStore: not implemented")
}

// Has is unimplemented; see package doc.
func (s *SQLiteStore) Has(path string) bool {
	panic("SQLiteStore: not implemented")
}

// Search is unimplemented; see package doc.
func (s *SQLiteStore) Search(queryVec []float32, k int) []Result {
	panic("SQLiteStore: not implemented")
}

// Save is unimplemented; see package doc.
func (s *SQLiteStore) Save() error {
	panic("SQLiteStore: not implemented")
}

// Close is unimplemented; see package doc.
func (s *SQLiteStore) Close() error {
	panic("SQLiteStore: not implemented")
}
