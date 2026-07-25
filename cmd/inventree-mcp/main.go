package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/config"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=$(git describe --always --dirty)"
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	c := client.New(cfg.URL, cfg.Token)
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
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	// Unauthenticated so container and reverse-proxy probes stay cheap. It
	// reports liveness only and exposes nothing about the InvenTree instance.
	mux.HandleFunc("/healthz", healthz)
	mux.Handle("/", requireToken(cfg.AuthToken, handler))

	srv := &http.Server{
		Addr:    cfg.Addr(),
		Handler: mux,
		// Guards against slow-header attacks. No WriteTimeout: Streamable HTTP
		// holds long-lived SSE responses open, which a write deadline would cut.
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Printf("inventree-mcp %s serving MCP over HTTP on %s", version, cfg.Addr())
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
	if auth := r.Header.Get("Authorization"); auth != "" {
		// Tolerate a missing "Bearer " prefix: the connector UI sends the header
		// value verbatim, so an admin who omits the scheme gets a working
		// connector rather than a silent 401.
		got = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
