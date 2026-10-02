package dockerdiscovery

import "testing"

func TestParseContainer(t *testing.T) {
	mkContainer := func(labels map[string]string) containerSummary {
		return containerSummary{ID: "abc123", Labels: labels}
	}

	t.Run("no caddy label", func(t *testing.T) {
		if svcs := parseContainer(mkContainer(nil)); len(svcs) != 0 {
			t.Fatalf("expected no service for a container without a caddy label, got %+v", svcs)
		}
	})

	t.Run("private service registered, name from first hostname label", func(t *testing.T) {
		svcs := parseContainer(mkContainer(map[string]string{
			labelCaddy: "web.shire.mesh", labelPrivate: "true",
		}))
		if len(svcs) != 1 {
			t.Fatalf("expected 1 service, got %+v", svcs)
		}
		if svcs[0].Name != "web" {
			t.Fatalf("unexpected service name: %+v", svcs[0])
		}
		if len(svcs[0].Access) != 1 || svcs[0].Access[0] != "*" {
			t.Fatalf("expected default access [*], got %v", svcs[0].Access)
		}
	})

	t.Run("explicit access overrides default", func(t *testing.T) {
		svcs := parseContainer(mkContainer(map[string]string{
			labelCaddy:   "web.shire.mesh",
			labelPrivate: "true",
			labelAccess:  "user:shire, user:bree",
		}))
		if len(svcs) != 1 {
			t.Fatalf("expected 1 service, got %+v", svcs)
		}
		want := []string{"user:shire", "user:bree"}
		if len(svcs[0].Access) != len(want) || svcs[0].Access[0] != want[0] || svcs[0].Access[1] != want[1] {
			t.Fatalf("unexpected access list: %v", svcs[0].Access)
		}
	})

	t.Run("scheme prefix stripped before taking the name", func(t *testing.T) {
		svcs := parseContainer(mkContainer(map[string]string{
			labelCaddy: "http://firefly.shire.mesh:8888", labelPrivate: "true",
		}))
		if len(svcs) != 1 {
			t.Fatalf("expected 1 service, got %+v", svcs)
		}
		if svcs[0].Name != "firefly" {
			t.Fatalf("expected name %q, got %q (scheme prefix leaked into the DNS name)", "firefly", svcs[0].Name)
		}
	})

	t.Run("unflagged (public) service is not registered", func(t *testing.T) {
		svcs := parseContainer(mkContainer(map[string]string{
			labelCaddy: "firefly-importer.anumeya.com",
		}))
		if len(svcs) != 0 {
			t.Fatalf("expected public services to be skipped — Caddy routes them directly, got %+v", svcs)
		}
	})

	t.Run("indexed caddy labels register one service per hostname", func(t *testing.T) {
		svcs := parseContainer(mkContainer(map[string]string{
			labelPrivate:            "true",
			"caddy_0":               "http://minio-api.shire.mesh:8888",
			"caddy_0.reverse_proxy": "{{upstreams 9000}}",
			"caddy_1":               "http://minio.shire.mesh:8888",
			"caddy_1.reverse_proxy": "{{upstreams 9001}}",
		}))
		if len(svcs) != 2 {
			t.Fatalf("expected 2 services, got %+v", svcs)
		}
		names := map[string]bool{svcs[0].Name: true, svcs[1].Name: true}
		if !names["minio-api"] || !names["minio"] {
			t.Fatalf("expected minio-api and minio, got %+v", svcs)
		}
	})
}
