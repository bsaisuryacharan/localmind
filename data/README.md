# Drop your notes here

Anything you put in this folder is indexed by `localmind-mcp`.

Supported types today: `.md`, `.markdown`, `.txt`, `.rst`.

The indexer rescans every 30 s. Once a file is indexed, you can ask Claude
(or any MCP-aware client) things like:

- *"What does my notes folder say about quarterly planning?"*
- *"Search my notes for anything mentioning sqlite-vec."*
- *"Read `meetings/2026-04-15.md` and summarize the action items."*

Subfolders are fine. Binary files (with NUL bytes early in the file) are
skipped automatically.
