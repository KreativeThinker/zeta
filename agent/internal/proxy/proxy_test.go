package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeHTTPWildcardAllowsAnyKnownPeer(t *testing.T) {
	m := New()
	m.Sync(
		[]ServiceConfig{{Name: "web", TargetAddr: "127.0.0.1:1", AllowedPKs: []string{"*"}}},
		map[string]string{"100.64.0.5": "some-unlisted-pubkey"},
		map[string]string{"100.64.0.5": "shire"},
	)

	req := httptest.NewRequest(http.MethodGet, "http://web.mesh/", nil)
	req.RemoteAddr = "100.64.0.5:5555"
	rr := httptest.NewRecorder()

	m.ServeHTTP(rr, req)

	// The reverse proxy will fail to dial 127.0.0.1:1, but a non-403/404
	// status proves the ACL check itself passed for an unlisted pubkey.
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
		t.Fatalf("wildcard route rejected known mesh peer: status %d", rr.Code)
	}
}

func TestServeHTTPWithoutWildcardDeniesUnlistedPeer(t *testing.T) {
	m := New()
	m.Sync(
		[]ServiceConfig{{Name: "web", TargetAddr: "127.0.0.1:1", AllowedPKs: []string{"some-other-pubkey"}}},
		map[string]string{"100.64.0.5": "some-unlisted-pubkey"},
		map[string]string{"100.64.0.5": "shire"},
	)

	req := httptest.NewRequest(http.MethodGet, "http://web.mesh/", nil)
	req.RemoteAddr = "100.64.0.5:5555"
	rr := httptest.NewRecorder()

	m.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unlisted pubkey without wildcard, got %d", rr.Code)
	}
}
