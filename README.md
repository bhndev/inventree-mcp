# InvenTree MCP Server

An [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) server that connects AI assistants to your [InvenTree](https://inventree.org/) inventory management system. Talk to your inventory in natural language — create parts, manage stock, organize locations, and more.

**Example prompts:**

- *"Add 10 ESP32-P4 boards to Green 1 in the office"*
- *"How many potentiometers do I have in stock?"*
- *"Create a new category for voltage regulators under Electronic Components"*
- *"Move 5 resistors from Blue 1 to Green 1"*
- *"Raise a purchase order to Digikey for 15 of these connectors"*
- *"Build 12 units of the FBT25m and tell me what stock it consumed"*

## Features

- **70 MCP tools** spanning parts, stock, locations, categories, purchase orders,
  sales orders, return orders, build orders, BOMs, companies and supplier parts
- **Full order lifecycles** — issue, receive, allocate, ship and complete, not just CRUD
- **Fuzzy search** — say "green box" and it finds "Green 1"
- **Hierarchical navigation** — locations and categories with full path display
- **Stock management** — add, remove, transfer, and track inventory
- **Two transports** — local stdio, or hosted over HTTPS as a custom connector
- **Per-user OAuth** — optionally forward each user's own token so InvenTree
  applies *their* role permissions rather than a shared service account's
- **Image search** — optionally find and attach product images via Google
- **Type coercion middleware** — handles client quirks gracefully

## Prerequisites

- **Go 1.23+** (for building from source)
- **InvenTree instance** (v1.x) accessible over HTTP/HTTPS
- **InvenTree API token** (see [Generating an API Token](#generating-an-api-token))

## Installation

### Build from source

```bash
git clone https://github.com/bhndev/inventree-mcp.git
cd inventree-mcp
go build -o inventree-mcp ./cmd/inventree-mcp
```

The binary is self-contained — copy it wherever you like.

### Verify it works

```bash
INVENTREE_URL=http://your-inventree-host \
INVENTREE_TOKEN=your-token-here \
./inventree-mcp
```

The server communicates over stdin/stdout using the MCP protocol. If it starts without errors, you're good. Press `Ctrl+C` to stop.

## Generating an API Token

The MCP server authenticates with InvenTree using an API token. There are two ways to get one:

### Option 1: From the InvenTree web UI

1. Log in to your InvenTree instance
2. Go to your user settings (click your username → **Settings**)
3. Find the **API Tokens** section
4. Create or copy your token

### Option 2: Programmatically

Send a GET request with your username and password using basic authentication:

```bash
curl -u your-username:your-password http://your-inventree-host/api/user/token/
```

Response:

```json
{
    "token": "inv-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-xxxxxxxx"
}
```

### Token notes

- Tokens are **persistent** — they don't expire unless an administrator revokes them
- API access is **scoped to user permissions** — the token inherits the permissions of the user it belongs to
- Use a dedicated user account for the MCP server if you want to restrict what the AI can do

## Configuration

The server is configured entirely via environment variables. **[`.env.example`](.env.example)
is the reference** — it documents every variable and lays out the three deployment
shapes as copy-and-uncomment sections.

The essentials:

| Variable | Required | Description |
|---|---|---|
| `INVENTREE_URL` | Yes | Base URL of your InvenTree instance (e.g., `http://192.168.1.100`) |
| `INVENTREE_TOKEN` | Usually | API token. Required for stdio and shared-secret modes; **not** used in OAuth mode |
| `MCP_TRANSPORT` | No | `stdio` (default) or `http` |
| `MCP_HOST` / `MCP_PORT` | No | Listener address in HTTP mode (`0.0.0.0` / `8000`) |
| `MCP_AUTH_MODE` | HTTP only | `token` (one shared secret) or `oauth` (per-user) |
| `MCP_AUTH_TOKEN` | If `token` | The shared secret — `openssl rand -hex 32` |
| `MCP_PUBLIC_URL` | If `oauth` | This server's canonical public URL; must exactly match what is typed into Claude |
| `OAUTH_ISSUER` | If `oauth` | InvenTree's issuer, `{host}/o` |
| `OAUTH_SCOPES` | If `oauth` | Scopes Claude requests; unset means *every* scope InvenTree advertises, including delete |
| `OAUTH_VERIFY` | No | `userinfo` (default) or `introspect` |
| `GOOGLE_API_KEY` | No | Google Cloud API key for image search |
| `GOOGLE_CSE_ID` | No | Google Custom Search Engine ID for image search |

`config.Load` validates at startup and refuses to run on an incomplete configuration —
notably it will not open an HTTP listener with no credential configured.

Real env files are gitignored (`.env`, `.env.*`, `*.env`). Never commit one; on a
server keep it at `chmod 600`.

### Optional: Image search

The `search_part_images` tool lets the AI find product photos and attach them to parts. To enable it:

1. Create a [Google Custom Search Engine](https://cse.google.com/) configured for image search
2. Enable the Custom Search API in your [Google Cloud Console](https://console.cloud.google.com/)
3. Set `GOOGLE_API_KEY` and `GOOGLE_CSE_ID`

If not configured, the server starts normally — only the image search tool will return an informative error. All other tools work regardless.

## Setup with Claude Code (CLI)

Add a `.mcp.json` file to your project root (or `~/.claude/.mcp.json` for global config):

```json
{
  "mcpServers": {
    "inventree": {
      "command": "/path/to/inventree-mcp",
      "env": {
        "INVENTREE_URL": "http://your-inventree-host",
        "INVENTREE_TOKEN": "your-token-here"
      }
    }
  }
}
```

> **Security:** Add `.mcp.json` to your `.gitignore` — it contains your API token.

After adding the config, restart Claude Code. The tools will be available automatically. You can verify with:

```
/mcp
```

This lists all connected MCP servers and their tools.

## Setup with Claude Desktop

Edit your Claude Desktop configuration file:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

Add the server to the `mcpServers` section:

```json
{
  "mcpServers": {
    "inventree": {
      "command": "/path/to/inventree-mcp",
      "env": {
        "INVENTREE_URL": "http://your-inventree-host",
        "INVENTREE_TOKEN": "your-token-here"
      }
    }
  }
}
```

Restart Claude Desktop. You should see a hammer icon indicating MCP tools are available.

## Available Tools

### Parts

| Tool | Description |
|---|---|
| `search_parts` | Search parts by name, keyword, or description |
| `get_part` | Get detailed info about a specific part |
| `list_parts` | List parts with optional category filter |
| `create_part` | Create a new part |
| `update_part` | Update part fields (name, description, category, etc.) |
| `delete_part` | Delete a part (auto-deactivates first) |
| `set_part_image` | Attach an image to a part via URL |
| `search_part_images` | Find product images via Google (requires API keys) |

### Stock

| Tool | Description |
|---|---|
| `get_stock` | List stock items with part/location filters |
| `get_stock_item` | Get a specific stock item by ID |
| `add_stock` | Create a new stock entry (part + quantity + location) |
| `stock_add_quantity` | Add quantity to existing stock items |
| `stock_remove_quantity` | Remove quantity from existing stock items |
| `stock_transfer` | Move stock between locations |
| `delete_stock_item` | Delete a stock entry |

### Stock Locations

| Tool | Description |
|---|---|
| `search_stock_locations` | Fuzzy search locations by name or description |
| `get_stock_location` | Get location details with full path |
| `list_stock_locations` | List all locations with hierarchy |
| `create_stock_location` | Create a new location (supports nesting) |
| `update_stock_location` | Update location fields |
| `delete_stock_location` | Delete an empty location |

### Part Categories

| Tool | Description |
|---|---|
| `search_part_categories` | Search categories by name |
| `list_part_categories` | List the full category hierarchy |
| `create_part_category` | Create a new category (supports nesting) |
| `update_part_category` | Update category fields |
| `delete_part_category` | Delete an empty category |

### Purchase Orders

| Tool | Description |
|---|---|
| `list_purchase_orders` | List purchase orders with optional filters |
| `get_purchase_order` | Get one order with its line items |
| `create_purchase_order` | Create an order against a supplier |
| `add_purchase_order_line` | Add a line (takes a **supplier** part) |
| `issue_purchase_order` | PENDING → PLACED |
| `receive_purchase_order` | Receive stock against the order |

### Sales Orders

| Tool | Description |
|---|---|
| `list_sales_orders` | List sales orders with optional filters |
| `get_sales_order` | Get one order with lines and shipments |
| `create_sales_order` | Create an order against a customer |
| `add_sales_order_line` | Add a line (takes an **internal** part, must be `salable`) |
| `issue_sales_order` | PENDING → IN PROGRESS |
| `create_sales_order_shipment` | Create a shipment — required before allocating |
| `allocate_sales_order_stock` | Reserve stock into a shipment |
| `ship_sales_order_shipment` | Dispatch — this is what removes stock |
| `complete_sales_order` | Close out the order |
| `cancel_sales_order` | Cancel the order |

### Return Orders

| Tool | Description |
|---|---|
| `list_return_orders` | List return orders with optional filters |
| `get_return_order` | Get one order with its line items |
| `create_return_order` | Create an RMA against a customer |
| `add_return_order_line` | Add a line (takes a **stock item** — the specific unit returning) |
| `issue_return_order` | PENDING → IN PROGRESS |
| `receive_return_order` | Receive returned items back into stock |
| `complete_return_order` | Close out the order |
| `cancel_return_order` | Cancel the order |

### Build Orders

| Tool | Description |
|---|---|
| `list_build_orders` | List build orders with optional filters |
| `get_build_order` | Get one build with allocation status |
| `create_build_order` | Create a build for an assembly part |
| `issue_build_order` | PENDING → PRODUCTION |
| `allocate_build_stock` | Allocate specific stock items to the build |
| `auto_allocate_build_stock` | Let InvenTree pick the stock (asynchronous) |
| `create_build_output` | Create the output unit(s) to be built |
| `complete_build_outputs` | Complete build **outputs** |
| `finish_build_order` | Complete the build **order** |
| `cancel_build_order` | Cancel the build (required before deletion) |

### Bill of Materials

| Tool | Description |
|---|---|
| `get_bom` | Get an assembly's BOM lines, with stock and buildable quantity |
| `add_bom_item` | Add a component line to a BOM |
| `update_bom_item` | Update a BOM line by its `pk` |
| `delete_bom_item` | Remove a BOM line |

### Companies & Supplier Parts

| Tool | Description |
|---|---|
| `search_companies` | Find suppliers, manufacturers and customers |
| `create_company` | Create a company (supplier / manufacturer / customer) |
| `search_supplier_parts` | Find supplier parts by SKU or internal part |
| `create_supplier_part` | Link a supplier's SKU to an internal part |
| `list_manufacturer_parts` | List manufacturer part records |
| `create_manufacturer_part` | Link a manufacturer's MPN to an internal part |

## Common Workflows

Several operations have ordering constraints that are not obvious from the tool
names. The tool descriptions carry this guidance for the model, but it is worth
knowing:

**Receiving ordered stock** — use `receive_purchase_order`, not `add_stock`. Only
the former keeps on-order quantities accurate.

**Building an assembly:**

```
get_bom → create_build_order → issue_build_order
        → auto_allocate_build_stock (or allocate_build_stock)
        → create_build_output → complete_build_outputs → finish_build_order
```

Skipping allocation makes completion fail with an opaque 400.

**Selling a part:**

```
update_part (salable=true, once per part) → create_sales_order → add_sales_order_line
        → issue_sales_order → create_sales_order_shipment
        → allocate_sales_order_stock → ship_sales_order_shipment → complete_sales_order
```

Allocation only *reserves* stock; shipping is what removes it.

**Two traps worth naming:**

- `complete_build_outputs` completes build **outputs**; `finish_build_order` completes
  the build **order**. Different operations on different objects, and the names invite
  exactly the wrong guess.
- Several writes are handled by a background worker, so HTTP 200 means *accepted*, not
  *done*. If the next call fails complaining about work you just did
  (`Required stock has not been fully allocated`), re-read and retry — do **not** reach
  for an `accept_*` override, which closes the order short instead.

**Bulk imports:** limit parallel writes to 3–5 at a time. InvenTree's default SQLite
backend uses file-level locking and concurrent writes surface as `OperationalError` 500s.
Order references are also auto-generated from InvenTree's reference pattern, which races
under concurrent creates — pass explicit references for batch work.

## How It Works

By default the server runs as a stdio process — the MCP client (Claude Code, Claude
Desktop) launches it and communicates via JSON-RPC over stdin/stdout.

```
User → Claude → MCP Client → inventree-mcp (stdio) → InvenTree REST API
```

It can also run as a hosted HTTPS service that any Claude surface connects to as a
custom connector. There are three shapes in total:

| Shape | `MCP_TRANSPORT` | `MCP_AUTH_MODE` | InvenTree calls made as |
|---|---|---|---|
| Local stdio (default) | `stdio` | n/a | the `INVENTREE_TOKEN` user |
| Hosted, shared secret | `http` | `token` | the `INVENTREE_TOKEN` user |
| Hosted, per-user OAuth | `http` | `oauth` | **the end user who made the request** |

In OAuth mode this server is an OAuth *resource server* only — it issues no tokens and
runs no authorization endpoints. InvenTree is the authorization server. Each call
carries the end user's own access token, which is forwarded to InvenTree, so InvenTree
enforces that user's role permissions rather than a service account's.

Two consequences worth knowing before deploying it:

- **The connector URL is `{MCP_PUBLIC_URL}/mcp`, not the bare origin.** The bare origin
  routes to the same handler, but OAuth compares what you typed into Claude against the
  `resource` this server advertises, and a mismatch fails the flow before anything else.
- **InvenTree's OIDC support is experimental and off by default.** Enable it with
  `INVENTREE_FLAGS={"OIDC": [{"condition": "boolean", "value": true}]}`. There is no
  Dynamic Client Registration, so you must pre-register a *confidential* client.

Hosted deployment — Compose layout, Caddy reverse proxy config, and a bisection
checklist for when OAuth fails — is documented in [CLAUDE.md](CLAUDE.md).

When you say something like *"Add 10 ESP32-P4 boards to Green 1"*, the AI:

1. Calls `search_parts("ESP32-P4")` to check if the part exists
2. Calls `search_stock_locations("Green 1")` to find the location
3. Creates the part with `create_part` if needed (after finding the right category)
4. Calls `add_stock(part=X, quantity=10, location=Y)` to add inventory

The AI handles disambiguation — if "green" matches multiple locations, it presents options. If a part doesn't exist, it asks whether to create it.

## Development

```bash
# Build all packages
go build ./...

# Build the binary
go build -o inventree-mcp ./cmd/inventree-mcp

# Run directly
INVENTREE_URL=http://... INVENTREE_TOKEN=... go run ./cmd/inventree-mcp

# Run integration tests (requires a live InvenTree instance)
INVENTREE_URL=http://... INVENTREE_TOKEN=... go test -v ./internal/tools/

# Run all tests
go test ./...
```

Or via the Makefile, which stamps the version from `git describe`:

```bash
make build      # compile with version stamped in
make install    # build to ~/.local/bin/inventree-mcp
make test       # go test ./...
make docker     # build the container image
```

After `make install`, quit and relaunch Claude Desktop — it only picks up a new binary
on restart. `/healthz` reports the running version, so "is my fix deployed?" is
answerable.

> **Note:** `go test ./...` passing is not proof the server works. `internal/client` has
> real unit tests, but `internal/tools/tools_integration_test.go` hits a live InvenTree
> instance and skips unless `INVENTREE_URL` and `INVENTREE_TOKEN` are set.

### Project structure

```
cmd/inventree-mcp/main.go     Entry point — config, client, server, RegisterAll, transport
internal/
  client/client.go             HTTP client for InvenTree REST API
  config/config.go             Environment variable configuration and validation
  auth/verify.go               OAuth bearer-token verification (resource server only)
  coerce/coerce.go             Type coercion middleware + the AddTool wrapper
  imagesearch/google.go        Google Custom Search image client (optional; may be nil)
  tools/
    parts.go                   Part CRUD + image tools
    stock.go                   Stock item management tools
    locations.go               Stock location tools
    categories.go              Part category tools
    purchase_orders.go         Purchase order tools
    sales_orders.go            Sales order tools
    return_orders.go           Return order tools
    builds.go                  Build order tools
    bom.go                     Bill of materials tools
    companies.go               Company tools
    supplier_parts.go          Supplier and manufacturer part tools
    references.go              Shared next-reference generator for all four order types
    register.go                Tool registration orchestrator
    tools_integration_test.go  Integration tests
```

### Adding a tool

Four things must line up, or the tool is broken or insecure. [CLAUDE.md](CLAUDE.md) has
the full detail; briefly:

1. Write `RegisterXxx(server *mcp.Server, c *client.Client, r *coerce.Registry)` in the
   file for its resource domain.
2. **Register with `coerce.AddTool`, never `mcp.AddTool` directly.** The wrapper records
   which JSON fields are numeric or boolean so the middleware can repair clients that
   send `"32"` instead of `32`.
3. Add the `RegisterXxx` call to `RegisterAll` in `internal/tools/register.go`.
4. **Start the handler body with `c := callerClient(c, req)`.** This is security-critical.
   In shared-token mode it returns the client unchanged; in OAuth mode it attaches the
   caller's credential. Omit it and the tool cannot make requests at all in OAuth mode —
   by design, so a handler that forgets fails loudly instead of silently escalating to a
   wider-permissioned credential.

To check the invariant still holds, compare the two counts — the difference should be
exactly one (`search_part_images` is the only tool with no InvenTree client):

```bash
grep -rc 'coerce\.AddTool'  internal/tools/*.go | awk -F: '{s+=$2} END {print s}'
grep -rc 'c := callerClient' internal/tools/*.go | awk -F: '{s+=$2} END {print s}'
```

## License

MIT
