# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

An MCP (Model Context Protocol) server in Go that exposes InvenTree inventory management API operations as MCP tools. Uses stdio transport for communication with MCP clients (e.g., Claude Code, Claude Desktop).

Module path: `github.com/chrisbotelho/inventree-mcp`

## Build & Test

```bash
go build ./...                            # compile all packages
go build -o inventree-mcp ./cmd/inventree-mcp
make install                              # build to ~/.local/bin/inventree-mcp
go test ./...                             # integration tests skip without credentials
```

Tests in `internal/tools/tools_integration_test.go` hit a **live InvenTree instance** and `t.Skip` unless both env vars are set:

```bash
INVENTREE_URL=http://... INVENTREE_TOKEN=... go test -v ./internal/tools/
INVENTREE_URL=... INVENTREE_TOKEN=... go test ./internal/tools/ -run TestSearchParts
```

After `make install`, quit and relaunch Claude Desktop — it only picks up the new binary on restart.

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
cmd/inventree-mcp/main.go   Entry point: config → client → server → RegisterAll → coercion middleware → stdio
internal/
  client/client.go          HTTP client for the InvenTree REST API (auth, Get/Post/Patch/Delete, error mapping)
  config/config.go          Env-var configuration
  coerce/coerce.go          Type-coercion middleware + the AddTool wrapper all tools register through
  imagesearch/google.go     Google Custom Search client (optional; may be nil)
  tools/                    One file per InvenTree resource domain, plus register.go
```

**Flow:** MCP client → stdio → `mcp.Server` → coercion middleware → tool handler → `client.Client` → InvenTree REST API

### Adding a tool

Three things must line up, or the tool silently won't work:

1. Write `RegisterXxx(server *mcp.Server, c *client.Client, r *coerce.Registry)` in the file for its resource domain (`parts.go`, `stock.go`, `purchase_orders.go`, …).
2. **Register with `coerce.AddTool`, never `mcp.AddTool` directly.** `coerce.AddTool` reflects over the input struct and records which JSON fields are integer/number/boolean, so the middleware can repair clients that send `"32"` instead of `32`. Registering via `mcp.AddTool` skips that and those clients get schema-validation failures.
3. Add the `RegisterXxx` call to `RegisterAll` in `internal/tools/register.go` — it is the single wiring point, and `main.go` installs the returned registry's middleware.

### Tool handler conventions

- Input structs use `json` + `jsonschema` tags; the `jsonschema` tag is the field's description shown to the model. Use `,omitempty` for optional fields and `*bool` where "unset" differs from `false`.
- **Return API failures as `errResult(err)` with a `nil` Go error**, not as the handler's error return. That surfaces the message to the model as tool output instead of a protocol error. Signature: `return errResult(fmt.Errorf("...: %w", err)), nil, nil`.
- Shared helpers `errResult`, `textResult`, `jsonResult`, `boolPtr` live at the bottom of `parts.go` and are used by every tool file.
- Set `mcp.ToolAnnotations` — `ReadOnlyHint: true` for queries, `DestructiveHint` for writes.
- Build payloads as `map[string]any`, conditionally adding keys, so omitted optional fields aren't sent as zero values that overwrite existing data.
- Append `&format=json` to GET paths and decode lists into `client.PaginatedResponse[T]`.
- Tool descriptions carry workflow guidance for the model (which tool to call first, ordering constraints, disambiguation rules). They are long on purpose — see `create_part` and `receive_purchase_order`.
- Validate required IDs/quantities in the handler and, where a precondition is knowable, check it with a GET first so the user gets a clear message instead of a bare 400 (see `receive_purchase_order`'s PLACED-status check).

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

### API quirks encoded in this codebase

- **Parts must be deactivated before deletion** — `delete_part` PATCHes `active: false` first.
- **Images are attached by URL, not upload** — PATCH `remote_image` and InvenTree fetches it server-side.
- **Purchase orders follow PENDING → PLACED → received.** `/api/order/po/{id}/receive/` rejects non-PLACED orders, and it returns an *array* while most endpoints return an object — decode into `any`.
- `client.Do` splits the query string off before `url.JoinPath` so `?` isn't percent-encoded.

## Configuration

The server expects `INVENTREE_URL` and `INVENTREE_TOKEN` environment variables to connect to an InvenTree instance.

### Optional: Image Search

To enable the `search_part_images` tool, set these additional environment variables:

- `GOOGLE_API_KEY` — Google Cloud API key with Custom Search API enabled
- `GOOGLE_CSE_ID` — Google Custom Search Engine ID (configured for image search)

If not set, the server starts normally but `search_part_images` returns an informative error. The `set_part_image`, `create_part` (with `image_url`), and `update_part` (with `image_url`) tools work regardless — they only need a direct image URL.

## Credential Safety

`client.Client`'s fields are deliberately unexported so the token can't leak via `fmt` output, logs, or JSON serialization, and `sanitizeError` redacts `Token ...` substrings from network errors. Keep both properties when touching `internal/client/`.

## Workflow Guidelines

- **Part descriptions from part numbers:** When the user provides just a part number (e.g., "LM7805", "ESP32-S3-WROOM-1"), look up or infer what the part is and generate a short, descriptive description for it. Never leave the description blank or just repeat the part number.

- **Bulk import throttling:** When creating multiple parts, stock items, or other resources via the InvenTree API, limit parallel calls to **3-5 at a time** and add a brief delay (`sleep 1`) between batches. InvenTree's default SQLite backend uses file-level locking, and too many concurrent writes cause `OperationalError` 500s. Always retry failed calls from a batch before moving on.

- **Receiving ordered stock:** use `receive_purchase_order`, not `add_stock` — only the former keeps on-order quantities accurate.
