package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chrisbotelho/inventree-mcp/internal/auth"
	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/config"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=$(git describe --always --dirty)"
var version = "dev"

// mcpPath is the canonical path the MCP endpoint is served on. It is part of
// the OAuth resource identifier, so it must match the URL entered in Claude.
const mcpPath = "/mcp"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	// In OAuth mode each InvenTree call carries the requesting user's own token,
	// so InvenTree enforces that user's role permissions. Otherwise every call
	// is attributed to the shared service account.
	c := client.New(cfg.URL, cfg.Token)
	if cfg.ForwardsCallerCredentials() {
		c = client.NewForwarding(cfg.URL)
	}
	imgClient := imagesearch.New(cfg.GoogleAPIKey, cfg.GoogleCSEID)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "inventree-mcp",
		Version: version,
	}, nil)

	registry := tools.RegisterAll(server, c, imgClient)
	server.AddReceivingMiddleware(registry.Middleware())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cfg.Transport {
	case config.TransportHTTP:
		err = runHTTP(ctx, cfg, server)
	default:
		err = server.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		log.Fatal(err)
	}
}

// runHTTP serves MCP over Streamable HTTP until ctx is cancelled.
func runHTTP(ctx context.Context, cfg *config.Config, server *mcp.Server) error {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	// Unauthenticated so container and reverse-proxy probes stay cheap. It
	// reports liveness only and exposes nothing about the InvenTree instance.
	mux.HandleFunc("/healthz", healthz)

	protected := installAuth(cfg, mux, mcpHandler)
	mux.Handle(mcpPath, protected)
	// Also accept the bare origin, so a connector configured without the /mcp
	// suffix still reaches the server rather than 404ing.
	mux.Handle("/", protected)

	srv := &http.Server{
		Addr:    cfg.Addr(),
		Handler: mux,
		// Guards against slow-header attacks. No WriteTimeout: Streamable HTTP
		// holds long-lived SSE responses open, which a write deadline would cut.
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Printf("inventree-mcp %s serving MCP over HTTP on %s (auth=%s)",
			version, cfg.Addr(), authLabel(cfg))
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// installAuth wraps the MCP handler in the configured authentication scheme,
// registering any discovery routes that scheme needs on mux.
func installAuth(cfg *config.Config, mux *http.ServeMux, next http.Handler) http.Handler {
	if cfg.AuthMode != config.AuthOAuth {
		return requireToken(cfg.AuthToken, next)
	}

	metadata := &oauthex.ProtectedResourceMetadata{
		// Must equal the URL entered in Claude, exactly (RFC 9728 §3.1).
		Resource:               cfg.PublicURL + mcpPath,
		AuthorizationServers:   []string{cfg.OAuthIssuer},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        cfg.OAuthScopes,
	}
	metadataHandler := mcpauth.ProtectedResourceMetadataHandler(metadata)
	// RFC 9728 §3.1: clients try the path-suffixed variant first when the
	// resource URL has a path component, then fall back to the bare path.
	mux.Handle("/.well-known/oauth-protected-resource", metadataHandler)
	mux.Handle("/.well-known/oauth-protected-resource"+mcpPath, metadataHandler)

	var verifier auth.Verifier = auth.NewUserInfoVerifier(cfg.OAuthUserInfoURL)
	// Scopes are advertised to Claude in either mode, but only introspection
	// reports the scopes a token actually carries, so only it can enforce them
	// here. Under UserInfo the check would compare against an empty set and
	// reject every request. InvenTree enforces scopes per endpoint regardless.
	var enforce []string
	if cfg.OAuthVerify == config.VerifyIntrospect {
		verifier = auth.NewIntrospector(
			cfg.OAuthIntrospectionURL, cfg.OAuthClientID, cfg.OAuthClientSecret)
		enforce = cfg.OAuthScopes
	}

	return mcpauth.RequireBearerToken(verifier.Verify, &mcpauth.RequireBearerTokenOptions{
		// The SDK interpolates this value into WWW-Authenticate without adding
		// quotes, so quote it here to emit the RFC 9728 form
		// (resource_metadata="https://…") rather than a bare URL.
		ResourceMetadataURL: strconv.Quote(cfg.ResourceMetadataURL()),
		Scopes:              enforce,
	})(next)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": version,
	})
}

// requireToken rejects any request that does not present the shared secret.
// Claude's custom-connector request headers allowlist includes both
// "authorization" and "x-api-key", so accept either.
func requireToken(want string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tokenValid(r, want) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_token",
				"error_description": "a valid bearer token is required",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tokenValid(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	if authz := r.Header.Get("Authorization"); authz != "" {
		// Tolerate a missing "Bearer " prefix: the connector UI sends the header
		// value verbatim, so an admin who omits the scheme gets a working
		// connector rather than a silent 401.
		got = strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// authLabel describes the active authentication scheme for the startup log.
func authLabel(cfg *config.Config) string {
	if cfg.AuthMode == config.AuthOAuth {
		return string(cfg.AuthMode) + "/" + string(cfg.OAuthVerify)
	}
	return string(cfg.AuthMode)
}
