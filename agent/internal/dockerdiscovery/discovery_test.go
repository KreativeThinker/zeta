package dockerdiscovery

import "testing"

func TestParseContainer(t *testing.T) {
	mkContainer := func(labels map[string]string) containerSummary {
		return containerSummary{ID: "abc123", Labels: labels}
	}

	t.Run("no caddy label", func(t *testing.T) {
		if _, ok := parseContainer(mkContainer(nil)); ok {
			t.Fatal("expected no service for a container without a caddy label")
		}
	})

	t.Run("private service registered, name from first hostname label", func(t *testing.T) {
		svc, ok := parseContainer(mkContainer(map[string]string{
			labelCaddy: "web.shire.mesh",
		}))
		if !ok {
			t.Fatal("expected service to be discovered")
		}
		if svc.Name != "web" {
			t.Fatalf("unexpected service name: %+v", svc)
		}
		if len(svc.Access) != 1 || svc.Access[0] != "*" {
			t.Fatalf("expected default access [*], got %v", svc.Access)
		}
	})

	t.Run("explicit access overrides default", func(t *testing.T) {
		svc, ok := parseContainer(mkContainer(map[string]string{
			labelCaddy:  "web.shire.mesh",
			labelAccess: "user:shire, user:bree",
		}))
		if !ok {
			t.Fatal("expected service to be discovered")
		}
		want := []string{"user:shire", "user:bree"}
		if len(svc.Access) != len(want) || svc.Access[0] != want[0] || svc.Access[1] != want[1] {
			t.Fatalf("unexpected access list: %v", svc.Access)
		}
	})

	t.Run("public service is not registered", func(t *testing.T) {
		if _, ok := parseContainer(mkContainer(map[string]string{
			labelCaddy:  "firefly-importer.anumeya.com",
			labelPublic: "true",
		})); ok {
			t.Fatal("expected public services to be skipped — Caddy routes them directly")
		}
	})
}
