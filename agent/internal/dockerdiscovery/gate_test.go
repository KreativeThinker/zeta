package dockerdiscovery

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const testCIDR = "100.64.0.0/10"

func TestGate(t *testing.T) {
	t.Run("private site forced to http and guarded", func(t *testing.T) {
		l := map[string]string{labelCaddy: "web.shire.mesh", labelPrivate: "true", "caddy.reverse_proxy": "x"}
		gate(l, testCIDR)
		if l[labelCaddy] != "http://web.shire.mesh" {
			t.Fatalf("address: %q", l[labelCaddy])
		}
		if l["caddy.@zeta_ext.not.remote_ip"] != testCIDR || l["caddy.abort"] != "@zeta_ext" {
			t.Fatalf("guard missing: %v", l)
		}
	})

	t.Run("indexed sites each guarded, existing scheme kept", func(t *testing.T) {
		l := map[string]string{"caddy_0": "https://a.shire.mesh", "caddy_1": "b.shire.mesh", labelPrivate: "true"}
		gate(l, testCIDR)
		if l["caddy_0"] != "https://a.shire.mesh" || l["caddy_1"] != "http://b.shire.mesh" {
			t.Fatalf("addresses: %v", l)
		}
		if l["caddy_0.abort"] == "" || l["caddy_1.abort"] == "" {
			t.Fatalf("guard missing: %v", l)
		}
	})

	t.Run("public untouched", func(t *testing.T) {
		l := map[string]string{labelCaddy: "x.example.com"}
		gate(l, testCIDR)
		if len(l) != 1 || l[labelCaddy] != "x.example.com" {
			t.Fatalf("mutated: %v", l)
		}
	})
}

func TestShim(t *testing.T) {
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"Id":"a","Names":["/a"],"Labels":{"caddy":"web.shire.mesh","zeta.private_network":"true"}},{"Id":"b","Labels":{"caddy":"x.example.com"}}]`)
	}))
	defer docker.Close()
	sock := filepath.Join(t.TempDir(), "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Watcher{hc: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", docker.Listener.Addr().String())
		},
	}}}
	if err := w.ServeShim(ctx, sock, testCIDR); err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}

	resp, err := c.Get("http://unix/v1.43/containers/json")
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		ID     string
		Names  []string
		Labels map[string]string
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got[0].Labels["caddy"] != "http://web.shire.mesh" || got[0].Labels["caddy.abort"] != "@zeta_ext" || got[0].Names[0] != "/a" {
		t.Fatalf("private container not gated / fields lost: %+v", got[0])
	}
	if got[1].Labels["caddy"] != "x.example.com" {
		t.Fatalf("public container altered: %+v", got[1])
	}

	resp, err = c.Post("http://unix/containers/create", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("write allowed: %d", resp.StatusCode)
	}
}
