package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureAuth starts a server that records the Authorization header it receives.
func captureAuth(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// A fixed-token client authenticates as the InvenTree service account, which
// uses the "Token" scheme rather than "Bearer".
func TestServiceAccountUsesTokenScheme(t *testing.T) {
	srv, got := captureAuth(t)

	var dest map[string]any
	if err := New(srv.URL, "svc-token").Get("/api/part/", &dest); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := "Token svc-token"; *got != want {
		t.Errorf("Authorization = %q, want %q", *got, want)
	}
}

// A forwarded OAuth access token must be sent as a bearer credential, so
// InvenTree attributes the call to the end user and applies their permissions.
func TestForwardedCallerTokenUsesBearerScheme(t *testing.T) {
	srv, got := captureAuth(t)

	var dest map[string]any
	c := NewForwarding(srv.URL).WithCallerToken("user-access-token")
	if err := c.Get("/api/part/", &dest); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := "Bearer user-access-token"; *got != want {
		t.Errorf("Authorization = %q, want %q", *got, want)
	}
}

// The guard that matters: a forwarding client with no caller token must refuse
// to make the request. Falling back to any other credential here would let a
// handler that forgot to derive a per-caller client act with wider permissions
// than the user actually holds.
func TestForwardingClientRefusesWithoutCallerToken(t *testing.T) {
	srv, got := captureAuth(t)

	var dest map[string]any
	err := NewForwarding(srv.URL).Get("/api/part/", &dest)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "no caller credential") {
		t.Errorf("error = %q, want it to mention the missing caller credential", err)
	}
	if *got != "" {
		t.Errorf("request reached the server with Authorization %q; it should not have been sent at all", *got)
	}
}

// Deriving a per-caller client must not mutate the shared base client, or one
// user's token could leak into another user's request.
func TestWithCallerTokenDoesNotMutateBase(t *testing.T) {
	base := NewForwarding("https://example.invalid")
	derived := base.WithCallerToken("user-a")

	if !base.RequiresCaller() {
		t.Error("base client no longer requires a caller credential")
	}
	if derived.RequiresCaller() {
		t.Error("derived client still requires a caller credential")
	}
	if base.token != "" {
		t.Errorf("base client picked up token %q", base.token)
	}
	if other := base.WithCallerToken("user-b"); other.token != "user-b" || derived.token != "user-a" {
		t.Errorf("tokens crossed: first=%q second=%q", derived.token, other.token)
	}
}

// serveStatus starts a server that replies with a fixed status, body and headers.
func serveStatus(t *testing.T, status int, body string, hdr map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A 403 has two causes needing opposite fixes. A missing OAuth scope is only
// fixable by reconnecting, so the error must say so rather than sending the
// operator to look at InvenTree roles that are already correct.
func TestForbiddenReportsInsufficientScope(t *testing.T) {
	srv := serveStatus(t, http.StatusForbidden, "", map[string]string{
		"WWW-Authenticate": `Bearer realm="api", error="insufficient_scope", scope="r:add:part"`,
	})

	err := New(srv.URL, "tok").Post("/api/part/", map[string]any{}, nil)
	if err == nil {
		t.Fatal("Post: expected error")
	}
	for _, want := range []string{"r:add:part", "reconnect"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The other cause is a missing InvenTree role, which reconnecting will not fix.
// DRF reports it as a "detail" string with no WWW-Authenticate challenge.
func TestForbiddenReportsRolePermission(t *testing.T) {
	srv := serveStatus(t, http.StatusForbidden,
		`{"detail":"You do not have permission to perform this action."}`, nil)

	err := New(srv.URL, "tok").Post("/api/part/", map[string]any{}, nil)
	if err == nil {
		t.Fatal("Post: expected error")
	}
	if !strings.Contains(err.Error(), "role permission") {
		t.Errorf("error %q should identify this as a role permission", err)
	}
	if strings.Contains(err.Error(), "reconnect") {
		t.Errorf("error %q wrongly suggests reconnecting", err)
	}
}
