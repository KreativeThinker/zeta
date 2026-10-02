package dockerdiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var containerJSONPath = regexp.MustCompile(`^(/v[0-9.]+)?/containers/(json|[^/]+/json)$`)

// ServeShim serves a read-only Docker API on sockPath with gate() applied to container labels.
func (w *Watcher) ServeShim(ctx context.Context, sockPath, meshCIDR string) error {
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return err
	}
	os.Remove(sockPath) //nolint:errcheck
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: newShim(w.hc.Transport, meshCIDR)}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	go srv.Serve(ln) //nolint:errcheck
	return nil
}

func newShim(docker http.RoundTripper, meshCIDR string) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "http"
			r.Out.URL.Host = "unix"
			r.Out.Header.Del("Accept-Encoding")
		},
		Transport:     docker,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode != http.StatusOK || !containerJSONPath.MatchString(resp.Request.URL.Path) {
				return nil
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}
			body = rewriteContainers(body, meshCIDR)
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
			resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
			return nil
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only docker proxy", http.StatusMethodNotAllowed)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// rewriteContainers gates a container list or inspect body; unparseable bodies pass through.
func rewriteContainers(body []byte, meshCIDR string) []byte {
	var list []map[string]json.RawMessage
	if json.Unmarshal(body, &list) == nil {
		for _, c := range list {
			gateRaw(c, "Labels", meshCIDR)
		}
		return mustMarshal(list, body)
	}
	var inspect map[string]json.RawMessage
	if json.Unmarshal(body, &inspect) != nil {
		return body
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(inspect["Config"], &cfg) != nil {
		return body
	}
	gateRaw(cfg, "Labels", meshCIDR)
	inspect["Config"] = mustMarshal(cfg, inspect["Config"])
	return mustMarshal(inspect, body)
}

func gateRaw(obj map[string]json.RawMessage, key, meshCIDR string) {
	var labels map[string]string
	if json.Unmarshal(obj[key], &labels) != nil || !isPrivate(labels) {
		return
	}
	gate(labels, meshCIDR)
	obj[key] = mustMarshal(labels, obj[key])
}

func mustMarshal(v any, fallback []byte) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return out
}
