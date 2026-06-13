# Zeta

A self-hostable mesh VPN built on WireGuard. Enroll any Linux device with a preauth key and it joins a private mesh network — traffic between peers is direct WireGuard tunnels, no coordinator in the data path.

```
┌──────────────────────────────┐
│        Controller            │  REST admin API  :8080
│  SQLite · CA · NetworkMap    │  gRPC sync       :50051
└──────────────┬───────────────┘
               │ control plane only
     ┌─────────┴──────────┐
     ▼                    ▼
┌─────────┐          ┌─────────┐
│ Agent A │◄────────►│ Agent B │   WireGuard  100.64.x.x/10
└─────────┘          └─────────┘   peer-to-peer data plane
```

The coordinator handles enrollment, key distribution, and network map updates. Once peers know each other's WireGuard keys, they communicate directly — the coordinator is not in the data path.

---

## Quick start

```bash
# 1. Start the controller
docker compose up -d controller

# 2. Create a preauth key
curl -s -X POST http://localhost:8080/api/v1/preauth-keys \
  -H 'Content-Type: application/json' \
  -d '{"label":"my-device","ttl_hours":24}' | jq -r '.key'

# 3. Enroll a device (run on the device, requires root)
sudo docker run --rm --privileged --network host \
  -v zeta-agent:/var/lib/zeta \
  ghcr.io/kreativethinker/zeta/agent:latest \
  --coordinator <controller-ip>:50051 \
  --preauth-key <key-from-step-2>
```

Admin UI: `http://localhost:8080`

---

## Documentation

- [Architecture](docs/architecture.md) — control plane vs data plane, enrollment flow, service announcement, DNS and proxy internals
- [Controller](docs/controller.md) — configuration, REST API reference, database schema
- [Agent](docs/agent.md) — configuration, zetafile service definitions, proxy, management API
- [Security](docs/security.md) — threat model, ACL enforcement, revocation, limitations
- [Deployment](docs/deployment.md) — Docker Compose, systemd, Caddy integration, production checklist

---

## Repository layout

```
zeta/
├── agent/          Linux agent
│   ├── cmd/agent/
│   └── internal/
│       ├── agentapi/ Agent management UI + REST API
│       ├── config/   YAML config, env overrides, zetafile
│       ├── control/  gRPC client + Sync stream
│       ├── dns/      In-process *.mesh DNS server
│       ├── nat/      STUN endpoint discovery (IPv4)
│       ├── proxy/    HTTP reverse proxy (Host-header routing + ACL)
│       ├── route/    netlink mesh route management
│       ├── state/    WG keypair + cert persistence
│       └── wg/       WireGuard interface management
├── controller/     Coordination server
│   ├── cmd/server/
│   └── internal/
│       ├── api/      gRPC + REST handlers, session auth
│       ├── ca/       ECDSA P-256 CA
│       ├── config/   YAML config + env overrides
│       ├── coordinator/  enrollment, NetworkMap, stream fan-out
│       └── db/       SQLite (devices, keys, services, audit)
├── frontend/       SvelteKit admin UI (embedded in controller binary)
├── proto/          Protobuf definitions + generated Go code
├── docs/           Detailed documentation
├── Makefile
└── docker-compose.yml
```
