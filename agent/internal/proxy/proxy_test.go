package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGatewayForwardsHostUnmodified verifies the gateway is a pure
// pass-through: it must forward to the configured Caddy address without
// rewriting the inbound Host header, since Caddy's own labels match on the
// full original hostname.
func TestGatewayForwardsHostUnmodified(t *testing.T) {
	var gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	g := New(backend.Listener.Addr().String())

	req := httptest.NewRequest(http.MethodGet, "http://myservice.shire.mesh/", nil)
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from backend, got %d", rr.Code)
	}
	if gotHost != "myservice.shire.mesh" {
		t.Fatalf("expected Host header passed through unmodified, got %q", gotHost)
	}
}
