package dns

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/kreativethinker/zeta/proto/zetapb"
	"github.com/miekg/dns"
)

type Resolver struct {
	mu       sync.RWMutex
	records  map[string]string // "hostname.mesh." → "100.64.0.x"
	upstream string
	server   *dns.Server
}

func New(listenAddr, upstream string) *Resolver {
	r := &Resolver{
		records:  make(map[string]string),
		upstream: upstream,
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", r.handle)
	r.server = &dns.Server{
		Addr:    listenAddr,
		Net:     "udp",
		Handler: mux,
	}
	return r
}

func (r *Resolver) UpdateFromNetworkMap(peers []*zetapb.Peer, domain string) {
	records := make(map[string]string, len(peers))
	for _, p := range peers {
		if p.Hostname == "" || p.MeshIp == "" {
			continue
		}
		// peer hostname: shire.mesh
		records[dns.Fqdn(fmt.Sprintf("%s.%s", p.Hostname, domain))] = p.MeshIp
		// per-service: web.shire.mesh
		for _, svc := range p.Services {
			fqdn := dns.Fqdn(fmt.Sprintf("%s.%s.%s", svc.Name, p.Hostname, domain))
			records[fqdn] = p.MeshIp
		}
	}
	r.mu.Lock()
	r.records = records
	r.mu.Unlock()
}

func (r *Resolver) Start() error {
	ready := make(chan struct{})
	r.server.NotifyStartedFunc = func() { close(ready) }
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.server.ListenAndServe()
	}()
	select {
	case <-ready:
		return nil
	case err := <-errCh:
		return err
	}
}

func (r *Resolver) Stop() error {
	return r.server.Shutdown()
}

func (r *Resolver) handle(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 0 {
		dns.HandleFailed(w, req)
		return
	}

	q := req.Question[0]
	if q.Qtype == dns.TypeA {
		name := strings.ToLower(q.Name)
		r.mu.RLock()
		ip, ok := r.records[name]
		r.mu.RUnlock()

		if ok {
			msg := new(dns.Msg)
			msg.SetReply(req)
			msg.Authoritative = true
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    60,
				},
				A: net.ParseIP(ip),
			})
			_ = w.WriteMsg(msg)
			return
		}
	}

	r.forward(w, req)
}

func (r *Resolver) forward(w dns.ResponseWriter, req *dns.Msg) {
	c := new(dns.Client)
	resp, _, err := c.Exchange(req, r.upstream)
	if err != nil {
		dns.HandleFailed(w, req)
		return
	}
	_ = w.WriteMsg(resp)
}
