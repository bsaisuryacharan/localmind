# Install

## Prerequisites

- Docker Engine 24+ and the `compose` plugin (`docker compose version` should print a version)
- 8 GB RAM minimum, 16 GB+ recommended
- ~20 GB free disk for models

## Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.sh | sh
localmind init
localmind up
```

Open <http://localhost:3000>.

## Windows

Run PowerShell as Administrator:

```powershell
iwr -useb https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.ps1 | iex
localmind init
localmind up
```

## From source

```bash
git clone https://github.com/localmind/localmind
cd localmind
( cd wizard && go build -o ../bin/localmind ./cmd/localmind )
./bin/localmind init
./bin/localmind up
```

## Uninstall

```bash
localmind down
docker volume rm localmind_ollama localmind_webui localmind_piper localmind_mcp_index
rm -rf ~/.localmind ~/.local/bin/localmind
```
