package nvoken

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A scope is worth nothing if it is only remembered locally, so this asserts
// what actually leaves the process: every request carries the headers, and a
// client derived from a scoped one keeps carrying them.
func TestScopedClientStampsEveryRequest(t *testing.T) {
	var observed []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed = append(observed, request.Header.Clone())
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"items":[],"has_more":false,"next_cursor":null}`))
	}))
	defer server.Close()

	unscoped, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := unscoped.ListSessions(context.Background(), ListSessionsOptions{}); err != nil {
		t.Fatalf("unscoped ListSessions: %v", err)
	}

	scoped, err := unscoped.Scoped(Scope{
		TenantKey: "acme",
		UserKey:   "user-7c1f",
	})
	if err != nil {
		t.Fatalf("Scoped: %v", err)
	}
	if _, err := scoped.ListSessions(context.Background(), ListSessionsOptions{}); err != nil {
		t.Fatalf("scoped ListSessions: %v", err)
	}

	constructed, err := NewClient(server.URL, "test-key", WithScope(Scope{TenantKey: "acme"}))
	if err != nil {
		t.Fatalf("NewClient with scope: %v", err)
	}
	if _, err := constructed.ListSessions(context.Background(), ListSessionsOptions{}); err != nil {
		t.Fatalf("constructed ListSessions: %v", err)
	}

	if len(observed) != 3 {
		t.Fatalf("requests = %d, want 3", len(observed))
	}
	if got := observed[0].Get("X-Nvoken-Tenant-Key"); got != "" {
		t.Errorf("unscoped tenant header = %q, want none", got)
	}
	if got := observed[0].Get("X-Nvoken-User-Key"); got != "" {
		t.Errorf("unscoped user header = %q, want none", got)
	}
	if got := observed[1].Get("X-Nvoken-Tenant-Key"); got != "acme" {
		t.Errorf("scoped tenant header = %q", got)
	}
	if got := observed[1].Get("X-Nvoken-User-Key"); got != "user-7c1f" {
		t.Errorf("scoped user header = %q", got)
	}
	if got := observed[2].Get("X-Nvoken-Tenant-Key"); got != "acme" {
		t.Errorf("constructed tenant header = %q", got)
	}
	if got := observed[2].Get("X-Nvoken-User-Key"); got != "" {
		t.Errorf("constructed user header = %q, want none", got)
	}

	// The receiver keeps its own scope, so handing a scoped client to one part
	// of an application cannot narrow the administrative one it came from.
	if unscoped.Scope() != (Scope{}) {
		t.Errorf("receiver scope = %#v, want zero", unscoped.Scope())
	}
	if scoped.Scope().TenantKey != "acme" || scoped.Scope().UserKey != "user-7c1f" {
		t.Errorf("scope = %#v", scoped.Scope())
	}
}

// An empty scope would stamp nothing while reading as a narrowing, which is the
// one failure mode a scope cannot have.
func TestScopedRefusesAScopeThatNamesNobody(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Scoped(Scope{}); err == nil {
		t.Fatal("Scoped with an empty scope: want error")
	}
	if _, err := client.Scoped(Scope{TenantKey: "   "}); err == nil {
		t.Fatal("Scoped with a blank tenant key: want error")
	}
}
