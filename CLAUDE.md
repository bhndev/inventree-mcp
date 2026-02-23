# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

An MCP (Model Context Protocol) server in Go that exposes InvenTree inventory management API operations as MCP tools. Uses stdio transport for communication with MCP clients (e.g., Claude Code, Claude Desktop).

## Build & Run

```bash
go build ./...                           # compile all packages
go build -o inventree-mcp ./cmd/inventree-mcp  # build the binary
go run ./cmd/inventree-mcp               # run directly
go test ./...                            # run all tests
go test ./internal/client/ -run TestGet  # run a single test
```

## Release Process

When asked to "build a release", "create a release", or "cut a release", follow these steps exactly:

1. **Cross-compile all platform binaries from the repo root:**
   ```bash
   GOOS=linux   GOARCH=amd64 go build -o inventree-mcp-linux-amd64        ./cmd/inventree-mcp
   GOOS=darwin  GOARCH=arm64 go build -o inventree-mcp-darwin-arm64        ./cmd/inventree-mcp
   GOOS=darwin  GOARCH=amd64 go build -o inventree-mcp-darwin-amd64        ./cmd/inventree-mcp
   GOOS=windows GOARCH=amd64 go build -o inventree-mcp-windows-amd64.exe   ./cmd/inventree-mcp
   ```

2. **Create a GitHub release** with `gh release create` attaching all four binaries. Use semver tags (v0.1.0, v0.2.0, etc.). Include a table mapping filenames to platforms and a summary of changes since the last release.

3. **Do NOT commit binaries to the repo** — they are gitignored. Releases are the distribution mechanism.

## Architecture

```
cmd/inventree-mcp/main.go   - Entry point. Creates the MCP server, registers tools, runs stdio transport.
internal/
  client/client.go           - HTTP client for InvenTree REST API. Handles auth (Token header) and request execution.
  tools/                     - MCP tool implementations (one file per InvenTree resource domain).
  config/                    - Configuration loading (base URL, API token).
```

**Flow:** MCP client → stdio → `mcp.Server` → tool handler → `client.Client` → InvenTree REST API

## InvenTree API

- **Base URL pattern:** `{host}/api/`
- **Auth:** `Authorization: Token <token>` header (get token via `GET /api/user/token/` with basic auth)
- **Docs:** https://docs.inventree.org/en/1.1.x/api/ and interactive schema at `{host}/api-doc/`
- **Key resource endpoints:**
  - `/api/part/` - Parts and categories
  - `/api/stock/` - Stock items and locations
  - `/api/build/` - Build/manufacturing orders
  - `/api/order/po/` - Purchase orders
  - `/api/order/so/` - Sales orders
  - `/api/order/ro/` - Return orders
  - `/api/bom/` - Bill of materials
  - `/api/company/` - Companies, suppliers, manufacturers
- All resources support standard CRUD. Many support `/metadata/` sub-endpoints and bulk operations.
- Pagination is Django REST Framework style.

## MCP SDK

Uses the official Go SDK: `github.com/modelcontextprotocol/go-sdk/mcp`

Register tools with:
```go
mcp.AddTool(server, &mcp.Tool{Name: "...", Description: "..."}, handlerFunc)
```

Handler signature uses typed input/output structs with `json`/`jsonschema` tags.

## Configuration

The server expects `INVENTREE_URL` and `INVENTREE_TOKEN` environment variables (or equivalent config) to connect to an InvenTree instance.

### Optional: Image Search

To enable the `search_part_images` tool, set these additional environment variables:

- `GOOGLE_API_KEY` — Google Cloud API key with Custom Search API enabled
- `GOOGLE_CSE_ID` — Google Custom Search Engine ID (configured for image search)

If not set, the server starts normally but `search_part_images` returns an informative error. The `set_part_image`, `create_part` (with `image_url`), and `update_part` (with `image_url`) tools work regardless — they only need a direct image URL.

## Workflow Guidelines

- **Part descriptions from part numbers:** When the user provides just a part number (e.g., "LM7805", "ESP32-S3-WROOM-1"), look up or infer what the part is and generate a short, descriptive description for it. Never leave the description blank or just repeat the part number.
