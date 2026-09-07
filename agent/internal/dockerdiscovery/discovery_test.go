package dockerdiscovery

import "testing"

func TestParseContainer(t *testing.T) {
	mkContainer := func(labels map[string]string, ip string) containerSummary {
		c := containerSummary{ID: "abc123", Labels: labels}
		c.NetworkSettings.Networks = map[string]struct {
			IPAddress string `json:"IPAddress"`
		}{
			"bridge": {IPAddress: ip},
		}
		return c
	}

	t.Run("no labels", func(t *testing.T) {
		if _, ok := parseContainer(mkContainer(nil, "172.17.0.2")); ok {
			t.Fatal("expected no service for unlabeled container")
		}
	})

	t.Run("default access is wildcard", func(t *testing.T) {
		svc, ok := parseContainer(mkContainer(map[string]string{
			labelName: "web",
			labelPort: "8080",
		}, "172.17.0.2"))
		if !ok {
			t.Fatal("expected service to be discovered")
		}
		if svc.Name != "web" || svc.Target != "172.17.0.2:8080" {
			t.Fatalf("unexpected service: %+v", svc)
		}
		if len(svc.Access) != 1 || svc.Access[0] != "*" {
			t.Fatalf("expected default access [*], got %v", svc.Access)
		}
	})

	t.Run("explicit access overrides default", func(t *testing.T) {
		svc, ok := parseContainer(mkContainer(map[string]string{
			labelName:   "web",
			labelPort:   "8080",
			labelAccess: "user:shire, user:bree",
		}, "172.17.0.2"))
		if !ok {
			t.Fatal("expected service to be discovered")
		}
		want := []string{"user:shire", "user:bree"}
		if len(svc.Access) != len(want) || svc.Access[0] != want[0] || svc.Access[1] != want[1] {
			t.Fatalf("unexpected access list: %v", svc.Access)
		}
	})

	t.Run("missing ip skips container", func(t *testing.T) {
		if _, ok := parseContainer(mkContainer(map[string]string{
			labelName: "web",
			labelPort: "8080",
		}, "")); ok {
			t.Fatal("expected no service when container has no network IP")
		}
	})
}
