# Zeta

A self-hostable Zero Trust Network Access (ZTNA) system built on WireGuard. Enroll any Linux device with a preauth key and it joins a private mesh network — all traffic is peer-to-peer after the coordinator hands out keys.

```
┌──────────────────────────────┐
│        Controller            │  REST admin API  :8080
│  SQLite · CA · NetworkMap    │  gRPC sync       :50051
└──────────────┬───────────────┘
               │ gRPC (enrollment + NetworkMap push)
     ┌─────────┴──────────┐
     ▼                    ▼
┌─────────┐          ┌─────────┐
│ Agent A │◄────────►│ Agent B │   WireGuard  100.64.x.x/10
└─────────┘          └─────────┘
```

- **Controller** — coordinates enrollment, allocates mesh IPs, signs device certs, fans out NetworkMap updates over a bidirectional gRPC stream.
- **Agent** — enrolls once with a preauth key, configures the `zeta0` WireGuard interface, and reconfigures its peers whenever the NetworkMap changes.

---

## Quick start (Docker)

```bash
# 1. Start the controller
docker compose up -d controller

# 2. Create a preauth key
curl -s -X POST http://localhost:8080/api/v1/preauth-keys \
  -H 'Content-Type: application/json' \
  -d '{"label":"laptop","ttl_hours":24}' | jq -r '.key'

# 3. Enroll a device (run on the device, requires root)
sudo docker run --rm --privileged --network host \
  -v zeta-agent:/var/lib/zeta \
  ghcr.io/kreativethinker/zeta/agent:latest \
  --coordinator <controller-ip>:50051 \
  --preauth-key <key-from-step-2>
```

Open `http://localhost:8080` for the admin UI.

---

## Installation

### Pre-built binaries

Download from the [Releases](https://github.com/KreativeThinker/zeta/releases) page.

```bash
# Controller
curl -Lo zeta-controller https://github.com/KreativeThinker/zeta/releases/latest/download/zeta-controller-linux-amd64
chmod +x zeta-controller
./zeta-controller

# Agent (requires root at runtime for WireGuard)
curl -Lo zeta-agent https://github.com/KreativeThinker/zeta/releases/latest/download/zeta-agent-linux-amd64
chmod +x zeta-agent
sudo ./zeta-agent --coordinator <controller-addr>:50051 --preauth-key <key>
```

### Build from source

**Prerequisites:** Go 1.22+, Node.js 20+, `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` (only if regenerating proto)

```bash
git clone https://github.com/KreativeThinker/zeta
cd zeta

# Install frontend deps and build everything
make ui-install
make build
# Binaries: dist/zeta-controller  dist/zeta-agent
```

Individual targets:

| Target | Description |
|---|---|
| `make controller` | Build controller (includes UI build) |
| `make agent` | Build Linux agent |
| `make controller-dev` | Run controller (no UI rebuild) |
| `make proto` | Regenerate Go code from proto |
| `make test` | Run all tests |
| `make clean` | Remove build artifacts |

---

## Controller

### Running

```bash
# Minimal — all defaults, data in ./zeta.db
./zeta-controller

# With config file
./zeta-controller --config /etc/zeta/zeta.yaml
```

### Config file (`zeta.yaml`)

All fields are optional; defaults shown below.

```yaml
db:
  path: zeta.db            # SQLite file path

grpc:
  addr: ":50051"           # agent enrollment + sync

http:
  addr: ":8080"            # REST API + admin UI

ca:
  cert_file: ""            # leave empty to store CA in DB
  key_file: ""

mesh:
  domain: mesh             # DNS zone: <hostname>.mesh
  cidr: "100.64.0.0/10"    # mesh IP pool (CGNAT range)
  controller_ip: "100.64.0.1"
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `ZETA_DB_PATH` | `zeta.db` | SQLite file path |
| `ZETA_GRPC_ADDR` | `:50051` | gRPC listen address |
| `ZETA_HTTP_ADDR` | `:8080` | HTTP listen address |
| `ZETA_CA_CERT_FILE` | — | Path to CA cert PEM (optional) |
| `ZETA_CA_KEY_FILE` | — | Path to CA key PEM (optional) |
| `ZETA_MESH_DOMAIN` | `mesh` | Mesh DNS zone |
| `ZETA_MESH_CIDR` | `100.64.0.0/10` | Mesh IP pool |
| `ZETA_MESH_CONTROLLER_IP` | `100.64.0.1` | Reserved controller IP |

### REST API

Base path: `http://<host>:8080/api/v1`

| Method | Path | Description |
|---|---|---|
| `GET` | `/status` | Version, uptime, device count |
| `GET` | `/devices` | List all devices with online flag |
| `GET` | `/devices/{id}` | Device details + services |
| `DELETE` | `/devices/{id}` | Remove device from mesh |
| `GET` | `/preauth-keys` | List enrollment keys |
| `POST` | `/preauth-keys` | Create enrollment key |
| `DELETE` | `/preauth-keys/{key}` | Delete enrollment key |
| `GET` | `/audit-log?limit=50&offset=0` | Audit log entries |

**Create preauth key:**

```bash
curl -X POST http://localhost:8080/api/v1/preauth-keys \
  -H 'Content-Type: application/json' \
  -d '{"label":"my-laptop","ttl_hours":24,"reusable":false}'
```

```json
{
  "key": "a3f9c2...",
  "label": "my-laptop",
  "reusable": false,
  "expiry": "2024-01-02T12:00:00Z",
  "used_at": null,
  "created_at": "2024-01-01T12:00:00Z"
}
```

---

## Agent

### Running

```bash
# First run: enroll (generates WireGuard keypair, saves state)
sudo ./zeta-agent \
  --coordinator 192.0.2.1:50051 \
  --preauth-key a3f9c2...

# Subsequent runs: state loaded from disk, no key needed
sudo ./zeta-agent --coordinator 192.0.2.1:50051
```

The agent requires **root** (or `CAP_NET_ADMIN`) to create the WireGuard interface and routes.

### Flags

| Flag | Description |
|---|---|
| `--coordinator` | Controller address (overrides config/env) |
| `--preauth-key` | Enrollment token (required on first run only) |
| `--config` | Path to YAML config file (optional) |

### Config file (`zeta-agent.yaml`)

```yaml
coordinator:
  addr: "localhost:50051"

wireguard:
  interface: zeta0
  listen_port: 51820

dns:
  listen_addr: "127.0.0.1:53"
  upstream: "1.1.1.1:53"

state:
  path: /var/lib/zeta/state.json
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `ZETA_COORDINATOR` | `localhost:50051` | Controller gRPC address |
| `ZETA_WG_INTERFACE` | `zeta0` | WireGuard interface name |
| `ZETA_STATE_PATH` | `/var/lib/zeta/state.json` | State file path |
| `ZETA_DNS_LISTEN` | `127.0.0.1:53` | DNS listen address |
| `ZETA_DNS_UPSTREAM` | `1.1.1.1:53` | Upstream DNS for non-mesh queries |

### Mesh networking

After enrollment, the agent:

1. Creates the `zeta0` WireGuard interface
2. Assigns `100.64.x.x/10` to the interface
3. Adds the `100.64.0.0/10` route via `zeta0`
4. Starts an in-process DNS server for `*.mesh` → mesh IP lookups
5. Opens a persistent gRPC stream to the controller; peer list is updated whenever any device joins, leaves, or changes endpoint

```bash
# Verify the interface is up
sudo wg show zeta0

# Verify mesh IP
ip addr show zeta0

# Verify route
ip route | grep 100.64

# Ping another enrolled device
ping 100.64.0.3

# DNS resolution
dig @127.0.0.1 my-laptop.mesh
```

---

## Docker deployment

### Controller only

```bash
docker run -d \
  --name zeta-controller \
  -p 8080:8080 \
  -p 50051:50051 \
  -v zeta-data:/data \
  -e ZETA_DB_PATH=/data/zeta.db \
  ghcr.io/kreativethinker/zeta/controller:latest
```

### Docker Compose (controller + demo)

```bash
# Clone and start
git clone https://github.com/KreativeThinker/zeta
cd zeta
docker compose up -d controller

# Tail logs
docker compose logs -f controller

# Create an enrollment key
docker compose exec controller \
  wget -qO- --post-data='{"label":"test","ttl_hours":1}' \
  --header='Content-Type: application/json' \
  http://localhost:8080/api/v1/preauth-keys
```

See `docker-compose.yml` in the repo root for the full reference configuration.

### Agent in Docker

The agent needs `--privileged` (or specific Linux capabilities) to manage WireGuard interfaces and routes.

```bash
docker run -d \
  --name zeta-agent \
  --privileged \
  --network host \
  -v zeta-agent-state:/var/lib/zeta \
  -e ZETA_COORDINATOR=<controller-ip>:50051 \
  ghcr.io/kreativethinker/zeta/agent:latest \
  --preauth-key <key>   # omit after first run
```

Minimum capabilities (instead of `--privileged`):

```bash
--cap-add NET_ADMIN \
--cap-add SYS_MODULE \
--device /dev/net/tun
```

---

## Architecture

```
Controller
├── SQLite DB (devices, preauth_keys, services, audit_log)
├── ECDSA P-256 CA (auto-generated, stored in DB)
├── gRPC CoordinatorService
│   ├── GetServerKey  — fetch CA public key
│   ├── Register      — enroll device with preauth key
│   └── Sync          — bidirectional stream (NetworkMap ← / Updates →)
└── REST API + SvelteKit admin UI

Agent
├── state.json (WG keypair, mesh IP, certs — persists across restarts)
├── zeta0 WireGuard interface
├── In-process DNS server (*.mesh zone)
└── gRPC sync stream (reconnects with exponential backoff)
```

### Enrollment flow

```
Agent                          Controller
  │── Register(wg_pubkey, ─────────────►│
  │           preauth_key,              │  validate key
  │           hostname, os) ────────────│  allocate 100.64.x.x
  │                                     │  issue device cert
  │◄── NodeConfig(node_id, ─────────────│
  │              mesh_ip,               │
  │              cert_pem, ca_pem) ─────│
  │                                     │
  │── OpenSync(node-id header) ─────────►│
  │◄── NetworkMap(all peers) ───────────│
  │                                     │
  │  [peer joins / leaves]              │
  │◄── NetworkMap(updated) ─────────────│
```

### Mesh IP range

Zeta uses the CGNAT range `100.64.0.0/10` (RFC 6598):

- `100.64.0.1` — reserved for the controller
- `100.64.0.2` — first enrolled device
- `100.64.0.3` — second enrolled device
- … up to ~4 million devices

---

## Development

```bash
# Start the controller in dev mode (no UI rebuild)
make controller-dev

# In a second terminal — frontend with hot reload
make ui-dev

# Run all tests
make test

# Regenerate proto bindings (requires protoc)
make proto
```

The controller's gRPC server runs in plaintext on `:50051` — usable with `grpcurl`:

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 zeta.v1.CoordinatorService/GetServerKey
```

---

## Repository layout

```
zeta/
├── agent/          Linux agent (WireGuard, gRPC client, DNS, STUN)
│   ├── cmd/agent/
│   └── internal/
│       ├── config/   YAML config + env overrides
│       ├── control/  gRPC client + Sync stream
│       ├── dns/      In-process *.mesh DNS server
│       ├── nat/      STUN endpoint discovery
│       ├── route/    netlink mesh route management
│       ├── state/    WG keypair persistence
│       └── wg/       WireGuard interface management
├── controller/     Coordination server
│   ├── cmd/server/
│   └── internal/
│       ├── api/      gRPC + REST handlers
│       ├── ca/       ECDSA P-256 CA
│       ├── config/   YAML config + env overrides
│       ├── coordinator/  enrollment, NetworkMap, stream fan-out
│       └── db/       SQLite (devices, keys, services, audit)
├── frontend/       SvelteKit admin UI (embedded in controller binary)
├── proto/          Protobuf definitions + generated Go code
├── Makefile
└── docker-compose.yml
```
