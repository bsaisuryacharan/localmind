// Package index manages the local file index that backs the MCP gateway.
//
// v0 is a stub: it advertises the tool descriptors and exposes a
// no-op Open/Close. The next change wires fsnotify + an embedding store
// backed by sqlite-vec.
package index

import "context"

type Config struct {
	DataDir        string
	IndexDir       string
	EmbeddingModel string
	OllamaBaseURL  string
}

type Index struct {
	cfg Config
}

func Open(_ context.Context, cfg Config) (*Index, error) {
	return &Index{cfg: cfg}, nil
}

func (i *Index) Close() error { return nil }

// ToolDescriptors returns MCP tool descriptors that this index exposes.
// Schemas are kept here (not in the HTTP layer) so that as the index
// gains capabilities the gateway picks them up automatically.
func (i *Index) ToolDescriptors() []map[string]any {
	return []map[string]any{
		{
			"name":        "search_files",
			"description": "Semantic search over files in the localmind data directory.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"k":     map[string]any{"type": "integer", "default": 8},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "list_files",
			"description": "List files currently known to the localmind index.",
			"inputSchema": map[string]any{"type": "object"},
		},
		{
			"name":        "read_file",
			"description": "Return the full contents of an indexed file.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
	}
}
