# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

An MCP (Model Context Protocol) server in Go that exposes InvenTree inventory management API operations as MCP tools. It runs either as a local stdio binary (the default, launched by Claude Desktop / Claude Code) or as a hosted HTTPS service that any Claude surface can connect to as a custom connector.

Module path: `github.com/chrisbotelho/inventree-mcp`

## Build & Test

```bash
go build ./...                            # compile all packages
make build                                # same, with version stamped in
make install                              # build to ~/.local/bin/inventree-mcp
make test                                 # go test ./...
make docker                               # build the container image
```

`go test ./...` passing is **not** proof the server works. `internal/client` has real unit tests, but `internal/tools/tools_integration_test.go` hits a live InvenTree instance and `t.Skip`s unless credentials are set:

```bash
INVENTREE_URL=http://... INVENTREE_TOKEN=... go test -v ./internal/tools/
INVENTREE_URL=... INVENTREE_TOKEN=... go test ./internal/tools/ -run TestSearchParts
```

After `make install`, quit and relaunch Claude Desktop — it only picks up the new binary on restart. In a container, `docker compose up -d --build inventree-mcp`.

Version is stamped via `-ldflags "-X main.version=..."`; the Makefile derives it from `git describe`. `/healthz` reports the running version, so "is my fix deployed?" is answerable.

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
cmd/inventree-mcp/main.go   Entry point: config → client → server → RegisterAll → transport
internal/
  client/client.go          HTTP client for the InvenTree REST API
  config/config.go          Env-var configuration and validation
  auth/verify.go            OAuth bearer-token verification (resource server only)
  coerce/coerce.go          Type-coercion middleware + the AddTool wrapper
  imagesearch/google.go     Google Custom Search client (optional; may be nil)
  tools/                    One file per InvenTree resource domain, plus register.go
    references.go           Shared next-reference generator for all four order types
```

**Flow:** MCP client → stdio or HTTP → `mcp.Server` → coercion middleware → tool handler → `client.Client` → InvenTree REST API

## Transports and authentication

Configuration is documented in `.env.example`, which is the reference for every variable. Three shapes:

| Shape | `MCP_TRANSPORT` | `MCP_AUTH_MODE` | InvenTree calls made as |
|---|---|---|---|
| Local stdio (default) | `stdio` | n/a | the `INVENTREE_TOKEN` user |
| Hosted, shared secret | `http` | `token` | the `INVENTREE_TOKEN` user |
| Hosted, per-user OAuth | `http` | `oauth` | **the end user who made the request** |

stdio is the default so existing local configs keep working after any change here.

### The forwarding model — read before touching tools or client

In OAuth mode this server is an OAuth **resource server** only: it issues no tokens and runs no authorization endpoints. InvenTree's django-oauth-toolkit is the authorization server. Claude's custom connectors accept a pre-registered client ID/secret, so there is no Dynamic Client Registration to implement.

Each MCP call carries the end user's own access token, and that token is forwarded to InvenTree, so **InvenTree applies that user's role permissions** rather than a service account's. Consequences:

- `client.New` holds a fixed token and sends `Authorization: Token <t>`.
- `client.NewForwarding` holds **no** credential. `WithCallerToken` derives a per-request copy that sends `Authorization: Bearer <t>`.
- A forwarding client with no caller token **refuses to issue the request**. This is deliberate: a handler that forgets to derive one fails loudly instead of silently escalating to a credential with wider permissions than the user holds. Do not add a fallback.

The caller's token is read from `req.Extra.Header`, **not** from `ctx`. The SDK attaches per-HTTP-request metadata to the JSON-RPC request (`streamable.go`: `jreq.Extra = &RequestExtra{TokenInfo, Header}`). Handler `ctx` derives from the session's original `initialize`, so a token taken from `ctx` would be frozen at connect time and go stale on refresh.

### Token verification

`internal/auth` offers two strategies, selected by `OAUTH_VERIFY`:

- **`userinfo` (default)** — presents the token to the OIDC UserInfo endpoint. Needs no client credentials, and a 200 proves the exact property forwarding depends on: that InvenTree accepts the token as an API bearer credential. Reports no expiry or scopes, so `Expiration` is set to the cache horizon and InvenTree remains the authority.
- **`introspect`** — RFC 7662. The only mode that reports a token's scopes, so the only one that can enforce them at this server. InvenTree does not advertise an introspection endpoint; confirm yours works before selecting it.

Both cache results for 60s, clamped to the token's expiry, so a tool call does not put a request on InvenTree every time.

## Adding a tool

Four things must line up, or the tool is broken or insecure:

1. Write `RegisterXxx(server *mcp.Server, c *client.Client, r *coerce.Registry)` in the file for its resource domain.
2. **Register with `coerce.AddTool`, never `mcp.AddTool` directly.** `coerce.AddTool` reflects over the input struct and records which JSON fields are integer/number/boolean, so the middleware can repair clients that send `"32"` instead of `32`. Registering via `mcp.AddTool` skips that and those clients get schema-validation failures.
3. Add the `RegisterXxx` call to `RegisterAll` in `internal/tools/register.go` — the single wiring point.
4. **Start the handler body with `c := callerClient(c, req)`.** This is security-critical, not boilerplate. In shared-token mode it returns the client unchanged; in OAuth mode it attaches the caller's credential. Omit it and the tool cannot make requests at all in OAuth mode (by design). All 69 handlers that talk to InvenTree do this; `search_part_images` is the one tool with no InvenTree client.

To check the invariant still holds after adding tools, compare the count of `coerce.AddTool` call sites against `c := callerClient` — the difference should be exactly one.

## Tool handler conventions

- Input structs use `json` + `jsonschema` tags; the `jsonschema` tag is the field's description shown to the model. Use `,omitempty` for optional fields and `*bool` where "unset" differs from `false`.
- **Return API failures as `errResult(err)` with a `nil` Go error**, not as the handler's error return. That surfaces the message to the model as tool output instead of a protocol error: `return errResult(fmt.Errorf("...: %w", err)), nil, nil`.
- Shared helpers `callerClient`, `bearerToken`, `errResult`, `textResult`, `jsonResult`, `boolPtr` live at the bottom of `parts.go` and are used by every tool file.
- Set `mcp.ToolAnnotations` — `ReadOnlyHint: true` for queries, `DestructiveHint` for writes.
- Build payloads as `map[string]any`, conditionally adding keys, so omitted optional fields aren't sent as zero values that overwrite existing data.
- Append `&format=json` to GET paths and decode lists into `client.PaginatedResponse[T]`.
- Tool descriptions carry workflow guidance for the model (which tool to call first, ordering constraints). They are long on purpose — see `create_part` and `receive_purchase_order`.
- Validate required IDs/quantities in the handler and, where a precondition is knowable, check it with a GET first so the user gets a clear message instead of a bare 400 (see `receive_purchase_order`'s PLACED-status check).

## InvenTree API

- **Base URL pattern:** `{host}/api/`
- **Auth:** `Authorization: Token <token>` for API tokens, `Bearer <token>` for OAuth access tokens. Both are accepted by the REST API.
- **Docs:** https://docs.inventree.org/en/1.1.x/api/ and interactive schema at `{host}/api-doc/`
- **Key resource endpoints:** `/api/part/`, `/api/stock/`, `/api/build/`, `/api/order/po/`, `/api/order/so/`, `/api/order/ro/`, `/api/bom/`, `/api/company/`
- All resources support standard CRUD. Many support `/metadata/` sub-endpoints and bulk operations.
- Pagination is Django REST Framework style.

### API quirks encoded in this codebase

- **Money fields are numbers, not strings** (`total_price: 0`, `purchase_price: 141.82`). A `*string` field fails to decode. Type only what the code needs internally and leave the rest untyped — see `poSummary` in `purchase_orders.go`.
- **`/api/order/po/{id}/receive/` returns an ARRAY**, not an object. Decode into `any`.
- **Parts must be deactivated before deletion** — `delete_part` PATCHes `active: false` first.
- **Images attach by URL, not upload** — PATCH `remote_image` and InvenTree fetches it server-side.
- **PO references are pattern-validated** (`PURCHASEORDER_REFERENCE_PATTERN`, default `PO-{ref:04d}`), so vendor strings are rejected; the vendor's own number goes in `supplier_reference`. `create_purchase_order` auto-generates a valid reference, which races under concurrent creates — pass explicit references for batch work.
- **Stock cannot be received against a PENDING order**; issue it first.
- **`/api/build/{id}/complete/` completes build OUTPUTS, `/finish/` completes the build ORDER.** They are different operations on different objects and the names invite exactly the wrong guess. `complete_build_outputs` and `finish_build_order` are named to keep the distinction visible to the model.
- **Sales order stock is allocated into a *shipment*, not directly against a line.** `allocate_sales_order_stock` therefore requires a shipment ID, and stock only leaves inventory when the shipment is dispatched with `ship_sales_order_shipment` — allocation alone changes nothing. InvenTree usually auto-creates shipment 1 with a new order, so check `get_sales_order` before creating another.
- **Line items reference a different object per order type.** PO lines take a *supplier* part, SO lines take an *internal* part, RO lines take a *stock item* (the specific unit coming back). Getting this wrong is the most common 400 in this area.
- **All four order types have their own reference pattern setting** (`PURCHASEORDER_`/`SALESORDER_`/`RETURNORDER_`/`BUILDORDER_REFERENCE_PATTERN`), and **these are user-configurable, not fixed defaults** — the test instance uses `RMA-{ref:04d}` for return orders, not `RO-`. `nextReference` in `references.go` therefore reads the pattern from `/api/settings/global/{KEY}/` and treats it as authoritative, using existing records only to find the highest number issued. Inferring the prefix from existing records alone fails for the *first* order of a type, when there is nothing to infer from. It still races under concurrent creates, so pass explicit references for batch work.
- **A sales order line only accepts a part marked `salable`.** InvenTree reports a non-salable part as `Invalid pk "N" - object does not exist`, which reads as though the part were missing. `add_sales_order_line` checks first and says what to fix; `create_part`/`update_part` expose the `salable` flag.
- **A new sales order has no shipment.** InvenTree does not auto-create one, so `create_sales_order_shipment` must be called before the first allocation.
- **Several write operations are performed by a background worker**, so a 200 means "accepted", not "done", and the next step in the workflow can fail if called immediately. Confirmed asynchronous: `auto-allocate/`, `complete/` (outputs), and sales order shipping. Confirmed **synchronous**: `allocate/` (manual build allocation), which reads back allocated immediately — so the two allocation paths behave differently and only the auto one needs a re-read. The symptom is a validation error naming work you just did — `Required stock has not been fully allocated`, `Required build quantity has not been completed`, `Order has incomplete line items`. The fix is to re-read and retry, **not** to set an `accept_*` override, which would close the order short instead. Verified: after `complete_build_outputs`, a build read `completed=0` and `finish_build_order` failed; a re-read moments later showed `completed=1/1` and the same call succeeded.
- **A build order must be cancelled before it can be deleted**, mirroring the deactivate-then-delete rule for parts.
- **`&` is rejected as HTML** in names — `{"name":["Remove HTML tags from this value"]}`.
- `client.Do` splits the query string off before `url.JoinPath` so `?` isn't percent-encoded.

### InvenTree OAuth findings

Measured against a live 1.4 instance. Re-verify before relying on any of it.

- OAuth2/OIDC is **experimental and off by default**; enable with `INVENTREE_FLAGS={"OIDC": [{"condition": "boolean", "value": true}]}`.
- Discovery lives at `{host}/o/.well-known/openid-configuration`; the issuer is `{host}/o`. The RFC 8414 path (`/.well-known/oauth-authorization-server/...`) 404s, but Claude accepts OIDC Discovery 1.0.
- **No `registration_endpoint` and no CIMD**, so DCR is unavailable — a pre-registered client ID/secret is the only option. `token_endpoint_auth_methods_supported` lacks `none`, so the client must be **confidential**.
- The application must have an **OIDC algorithm** (e.g. RS256) selected, or the `openid` scope is unavailable and UserInfo verification fails.
- Refresh tokens **are** issued even though `offline_access` is absent from `scopes_supported`.
- **Scopes were not observed to be enforced** by the API: a token granted only `openid g:read r:view:part` still read `/api/order/po/`. That test ran as a superuser, so it does not distinguish superuser bypass from no gating at all. Until settled, treat the connected user's **role permissions** as the real control, and do not connect Claude as a superuser.
- **Widening `OAUTH_SCOPES` does nothing until the connector is re-added.** A token carries the scopes granted at consent, and a refresh renews that same set rather than widening it, so a client connected under an older list keeps hitting 403 on every newly-added scope while reads keep working. Symptom: reads succeed, writes fail, and `/.well-known/oauth-protected-resource` already lists the write scope. Fix by disconnecting and re-adding the connector in Claude — not by editing InvenTree roles, which are already correct in this case.
- **A 403 has two causes needing opposite fixes**, and they are only distinguishable from the response: a missing scope comes back with `WWW-Authenticate: ... error="insufficient_scope"` (fix by reconnecting), a missing InvenTree role comes back as DRF's `{"detail": ...}` with no challenge (fix by granting the role). `client.apiError` reports whichever is present; do not collapse either back into a canned string.
- **"Hash client secret" makes the plaintext unrecoverable.** The registration form pre-fills a generated secret; copy it *before* saving. Afterwards the field shows a Django digest (`pbkdf2_sha256$<iterations>$<salt>$<digest>`), and pasting that into a client fails the token exchange as `invalid_client`. A usable secret is ~128 characters of mixed alphanumerics and contains **no `$`**. Hashing works correctly at the token endpoint — the only cost is that the value can never be read back, so leave it unchecked if you would rather retrieve it later than regenerate.

## Hosted deployment

Applies when running behind a reverse proxy as a custom connector.

**The connector URL is `{MCP_PUBLIC_URL}/mcp`, not the bare origin.** The bare origin routes to the same handler, but OAuth compares the URL entered in the client against the `resource` field this server advertises, and a mismatch fails the flow. Confirm they agree before debugging anything else:

```bash
curl -s https://mcp.example.com/.well-known/oauth-protected-resource
```

### Compose, alongside an existing InvenTree stack

Add the service in a **`docker-compose.override.yml`** next to InvenTree's own compose file. Compose merges it automatically, InvenTree's file stays untouched by upgrades, and the container joins the same project network — which is what lets the proxy reach it by name.

```yaml
services:
  inventree-mcp:
    build:
      context: /root/inventree-mcp     # a clone of this repo
      args:
        VERSION: ${MCP_VERSION:-dev}
    image: inventree-mcp:local
    container_name: inventree-mcp
    restart: unless-stopped
    env_file:
      - mcp.env                        # chmod 600; see .env.example
    expose:
      - "8000"
```

Deliberate omissions:

- **No `ports:`** — `expose` only. The proxy owns the public interface; publishing a host port would put the endpoint on the public interface directly.
- **No `depends_on:`** — this server opens no connection to InvenTree until a request arrives, so ordering buys nothing. It also avoids a trap: `depends_on` takes a *service* name, which is not necessarily the `container_name` shown by `docker ps`.

Verify the merge before starting anything — if the service is absent, the override filename or location is wrong:

```bash
docker compose config | awk '/^  inventree-mcp:/,/^  [a-z-]+:$/' | grep -vi 'password\|secret\|token'
```

**Never paste raw `docker compose config` output anywhere.** It renders every resolved environment variable, including admin passwords and API tokens belonging to neighbouring services. Filter it, as above.

### Reverse proxy (Caddy)

A new site block for the MCP host:

```caddy
mcp.example.com {
        log {
                output file /var/log/caddy/inventree-mcp.access.log
        }

        # Streamable HTTP holds long-lived SSE responses open. Disable
        # response buffering so events reach the client as produced.
        reverse_proxy inventree-mcp:8000 {
                flush_interval -1
        }
}
```

Note what is **absent**: no `/.well-known/*` handling. That is where the protected resource metadata lives, and blocking it breaks OAuth entirely. An InvenTree proxy config may legitimately 404 those paths to stop probes falling through to its HTML catch-all — that rule belongs on the InvenTree host, never on this one.

On the **InvenTree** site block, two adjustments help clients discover the authorization server:

```caddy
inventree.example.com {
        # ... existing log / request_body / encode / static / media ...

        # RFC 8414 discovery. InvenTree publishes OIDC discovery only at
        # /o/.well-known/openid-configuration, so serve that same document
        # here for clients that look in the RFC 8414 location instead.
        handle /.well-known/oauth-authorization-server* {
                rewrite * /o/.well-known/openid-configuration
                reverse_proxy {$INVENTREE_SERVER:"http://inventree-server:8000"}
        }

        # This host is not an MCP server. Answer cleanly rather than letting
        # probes fall through to InvenTree's HTML catch-all.
        handle /.well-known/oauth-protected-resource* {
                respond 404
        }

        handle {
                reverse_proxy {$INVENTREE_SERVER:"http://inventree-server:8000"}
        }
}
```

The rewrite matters because a bare `respond 404` on `oauth-authorization-server*` also swallows the path-suffixed form (`/.well-known/oauth-authorization-server/o`), which is exactly where an RFC 8414 client looks. Serving the OIDC document there satisfies both conventions; the `issuer` inside it validates either way.

Add the DNS record for the MCP host **before** reloading, or the ACME challenge for the new block cannot complete. Apply with:

```bash
docker compose up -d --force-recreate inventree-proxy
```

`--force-recreate`, not `caddy reload`: the Caddyfile is typically a single-file bind mount, and an editor that writes-and-renames leaves the container holding the old inode, so a reload silently re-reads stale content.

## When OAuth fails

The client reports one opaque message for every failure, so bisect from the outside in. Everything below is unauthenticated:

```bash
curl -s https://mcp.example.com/healthz                                       # process alive
curl -s https://mcp.example.com/.well-known/oauth-protected-resource          # resource + issuer
curl -sD- -o /dev/null -X POST https://mcp.example.com/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'             # 401 + WWW-Authenticate
curl -s https://inventree.example.com/o/.well-known/openid-configuration      # issuer reachable
```

If all four are correct, the fault is in the authorization server's application registration, not this server — this server logs nothing on a successful verification, so anything in its log during an attempt is the real error. In rough order of likelihood: the client secret is a hash rather than the plaintext; the redirect URI does not exactly match the client's callback; the application has no OIDC algorithm, so `openid` cannot be granted; or the client is registered public when the token endpoint requires a secret.

To find out which, replay the authorization request directly against `/o/authorize/` with the same `response_type`, `client_id`, `redirect_uri`, `scope`, and `code_challenge_method=S256`. A redirect to a login page means the parameters were accepted and the fault is later, in the token exchange. A 400 names the offending parameter. This needs only the client ID — no secret, no password.

## SDK details worth knowing

Uses `github.com/modelcontextprotocol/go-sdk`. Three behaviours that cost time to discover:

- `auth.RequireBearerToken` **rejects a `TokenInfo` with a zero `Expiration`**, so any verifier must populate it.
- It interpolates `ResourceMetadataURL` into `WWW-Authenticate` **without quoting**, so the value is pre-quoted with `strconv.Quote` to emit the RFC 9728 form.
- Per-request auth data arrives on `req.Extra`, not `ctx` (see the forwarding model above).

The MCP endpoint is served at **`/mcp`** (the bare origin also routes there). That path is part of the OAuth resource identifier advertised in the protected resource metadata, so changing it changes what must be typed into Claude.

## Configuration

See `.env.example` for every variable and the three deployment shapes. Real env files are gitignored (`.env`, `.env.*`, `*.env`); never commit one.

`config.Load` reads environment variables only and refuses to start on an incomplete configuration — notably it will not open an HTTP listener without a credential configured.

## Credential Safety

`client.Client`'s fields are deliberately unexported so tokens can't leak via `fmt` output, logs, or JSON serialization, and `sanitizeError` redacts `Token ...` substrings from network errors. Keep both properties when touching `internal/client/`.

The forwarding client's refusal to act without a caller credential is a security property, not an inconvenience — see "The forwarding model" above.

## Workflow Guidelines

- **Part descriptions from part numbers:** When the user provides just a part number (e.g., "LM7805", "ESP32-S3-WROOM-1"), look up or infer what the part is and generate a short, descriptive description for it. Never leave the description blank or just repeat the part number.

- **Bulk import throttling:** When creating multiple parts, stock items, or other resources via the InvenTree API, limit parallel calls to **3-5 at a time** and add a brief delay (`sleep 1`) between batches. InvenTree's default SQLite backend uses file-level locking, and too many concurrent writes cause `OperationalError` 500s. Always retry failed calls from a batch before moving on.

- **Receiving ordered stock:** use `receive_purchase_order`, not `add_stock` — only the former keeps on-order quantities accurate.

- **Building an assembly:** the order is `get_bom` (confirm the assembly has one) → `create_build_order` → `issue_build_order` → `auto_allocate_build_stock` or `allocate_build_stock` → `create_build_output` → `complete_build_outputs` → `finish_build_order`. Skipping allocation makes completion fail, which reads as an opaque 400.

- **Selling a part:** `update_part` with `salable=true` (once per part) → `create_sales_order` → `add_sales_order_line` → `issue_sales_order` → `create_sales_order_shipment` → `allocate_sales_order_stock` → `ship_sales_order_shipment` → `complete_sales_order`. Allocation reserves stock; only shipping removes it.

- **Verification status:** all four domains were exercised end to end against a live 1.4 instance — build (BOM → build order → issue → auto-allocate → output → complete → finish, yielding real stock), sales (through ship and complete), return (through receive and complete), and BOM add/update/delete. The build routes were additionally confirmed against InvenTree's `build/api.py`. Not yet exercised: `allocate_build_stock` (only the auto-allocate path was used), partial/split shipments, serialised build outputs, and the `accept_*` override flags.
