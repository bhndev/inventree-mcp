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
4. **Start the handler body with `c := callerClient(c, req)`.** This is security-critical, not boilerplate. In shared-token mode it returns the client unchanged; in OAuth mode it attaches the caller's credential. Omit it and the tool cannot make requests at all in OAuth mode (by design). All 37 handlers that talk to InvenTree do this; `search_part_images` is the one tool with no InvenTree client.

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
- **"Hash client secret" makes the plaintext unrecoverable.** The registration form pre-fills a generated secret; copy it *before* saving. Afterwards the field shows a Django digest (`pbkdf2_sha256$<iterations>$<salt>$<digest>`), and pasting that into a client fails the token exchange as `invalid_client`. A usable secret is ~128 characters of mixed alphanumerics and contains **no `$`**. Hashing works correctly at the token endpoint — the only cost is that the value can never be read back, so leave it unchecked if you would rather retrieve it later than regenerate.

## Hosted deployment

Applies when running behind a reverse proxy as a custom connector.

**The connector URL is `{MCP_PUBLIC_URL}/mcp`, not the bare origin.** The bare origin routes to the same handler, but OAuth compares the URL entered in the client against the `resource` field this server advertises, and a mismatch fails the flow. Confirm they agree before debugging anything else:

```bash
curl -s https://mcp.example.com/.well-known/oauth-protected-resource
```

**Reverse proxy requirements:**

- Do **not** block or 404 `/.well-known/*` on the MCP host. That is where the protected resource metadata lives, and blocking it breaks OAuth entirely. Some InvenTree proxy configs 404 those paths to stop probes falling through to the HTML catch-all — that rule belongs on the InvenTree host, never on this one.
- Streamable HTTP holds long-lived SSE responses open, so response buffering must be off. In Caddy: `reverse_proxy inventree-mcp:8000 { flush_interval -1 }`.
- If the authorization server sits behind the same proxy, serve its discovery document at the RFC 8414 path too. InvenTree only publishes OIDC discovery at `/o/.well-known/openid-configuration`; a client looking in the RFC 8414 location (`/.well-known/oauth-authorization-server/o`) gets nothing. An internal rewrite to the OIDC document satisfies both, and the `issuer` inside it validates either way.

**Alongside an existing stack:** add the service in a `docker-compose.override.yml` rather than editing a vendored `docker-compose.yml`, which upgrades can overwrite. Use `expose`, not `ports` — the proxy owns the public interface. `depends_on` is unnecessary: this server opens no connection to InvenTree until a request arrives.

**Never paste `docker compose config` output.** It renders every resolved environment variable, including admin passwords and API tokens from neighbouring services.

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
