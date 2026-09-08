# Agent

The agent runs on each device in the mesh. It manages the WireGuard interface, resolves mesh DNS names, exposes local services via an HTTP proxy, and maintains a sync stream with the controller.

Requires **root** or `CAP_NET_ADMIN` to create WireGuard interfaces and manage routes.

---

## Running

```bash
# First run: enroll with a preauth key
sudo ./zeta-agent \
  --coordinator 192.0.2.1:50051 \
  --preauth-key a3f9c2...

# Subsequent runs: loads saved state, no key needed
sudo ./zeta-agent --coordinator 192.0.2.1:50051
```

---

## Flags

| Flag | Description |
|---|---|
| `--coordinator` | Controller gRPC address (overrides config and env) |
| `--preauth-key` | Enrollment token — required on first run, ignored after |
| `--config` | Path to `zeta-agent.yaml` (optional) |
| `--zetafile` | Path to `zetafile.yml` (default: `./zetafile.yml`) |

---

## Configuration

### Config file (`zeta-agent.yaml`)

All fields are optional; defaults shown.

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

http:
  addr: "127.0.0.1:6080"   # agent management UI

proxy:
  addr: "0.0.0.0:1080"     # mesh service proxy
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `ZETA_COORDINATOR` | `localhost:50051` | Controller gRPC address |
| `ZETA_WG_INTERFACE` | `zeta0` | WireGuard interface name |
| `ZETA_STATE_PATH` | `/var/lib/zeta/state.json` | State file path |
| `ZETA_DNS_LISTEN` | `127.0.0.1:53` | DNS listen address |
| `ZETA_DNS_UPSTREAM` | `1.1.1.1:53` | Upstream DNS for non-mesh queries |
| `ZETA_HTTP_ADDR` | `127.0.0.1:6080` | Agent management UI |
| `ZETA_PROXY_ADDR` | `0.0.0.0:1080` | Mesh service proxy |

---

## State file

`state.json` is written once at enrollment and read on every subsequent start. It contains:

- WireGuard private and public key pair
- Assigned mesh IP (`100.64.x.x`)
- Mesh domain (e.g. `shire.mesh`)
- Device certificate and private key (PEM, issued by mesh CA)
- CA certificate (PEM)

**Do not delete this file** — losing it requires re-enrollment with a new preauth key, and the old device record should be deleted from the controller to reclaim the hostname.

---

## WireGuard interface

The agent creates and manages the `zeta0` WireGuard interface (name configurable via `ZETA_WG_INTERFACE`).

On startup:
1. Creates the interface if it does not exist (`ip link add zeta0 type wireguard`)
2. Sets the private key and listen port
3. Assigns the mesh IP with a `/10` prefix mask
4. Adds the `100.64.0.0/10` route via `zeta0`

On each NetworkMap update:
- Applies the full peer list via `wg set` (WireGuard handles diffing internally)
- Each peer entry: `PublicKey`, `AllowedIPs = <mesh_ip>/32`, `Endpoint = <stun_ip>:<port>`

On shutdown:
- Clears all WireGuard peers
- Removes the `100.64.0.0/10` route

Verify:

```bash
sudo wg show zeta0
ip addr show zeta0
ip route | grep 100.64
```

---

## DNS resolver

The agent runs an in-process DNS server (default `127.0.0.1:53`) and configures `systemd-resolved` to use it for the `.mesh` zone via a drop-in at `/etc/systemd/resolved.conf.d/zeta.conf`:

```ini
[Resolve]
DNS=127.0.0.1
Domains=~mesh
```

This means `.mesh` queries are routed to the agent's DNS server; everything else goes to the system resolver unchanged.

### Resolved names

| Query | Answer |
|---|---|
| `shire.mesh` | `100.64.0.2` (shire's mesh IP) |
| `files.shire.mesh` | `100.64.0.2` (same IP — proxy routes by Host header) |

The DNS records are rebuilt on every NetworkMap push. Only `A` records are served; `AAAA` queries fall through to upstream.

---

## Proxy gateway

Caddy (`lucaslorentz/caddy-docker-proxy`) is the only reverse proxy — it reads
container labels directly off the Docker socket and does all HTTP routing,
for both public and private services. The agent's own proxy is now a thin
pass-through in front of it: it listens on a single TCP port (default
`0.0.0.0:1080`) and forwards every request, `Host` header untouched, to
Caddy's private-only bind (default `127.0.0.1:8888`).

### Public vs private

Each service container carries a `caddy` label with its full hostname, plus
an optional `zeta.public` flag:

```yaml
labels:
  caddy: files.shire.mesh
  caddy.reverse_proxy: "{{upstreams 9010}}"
  zeta.access: user:graveyard,user:laptop
```

- **Private (default)** — no `zeta.public` label. Caddy binds this site to
  `127.0.0.1:8888` only, reachable exclusively through the agent's gateway on
  1080. This is the same boundary the old proxy enforced — 1080 is the real
  entry point, and the host firewall (`agent/internal/firewall`, opt-in) is
  what keeps it off the public interface.
- **Public** (`zeta.public: "true"`) — Caddy binds this site to
  `0.0.0.0:443`/`:80` with automatic HTTPS. Internet clients hit Caddy
  directly; the agent's gateway is never involved.

Only private services are registered with zeta's mesh DNS (the service name
is the first label of the `caddy` hostname). Public services use their real
domain and Caddy's own ACME — zeta doesn't need to know about them at all.

### ACL enforcement — not yet wired up

`zeta.access` is parsed and carried through to the controller, but nothing
currently enforces it — any mesh peer that resolves a private service's
hostname can reach it through Caddy. This mirrors an existing gap: the mesh
itself has no port-level restriction between peers (any peer can already dial
any peer's port directly — see `docs/DISTRIBUTED_ARCHITECTURE_PLAN.md`), so
this isn't a new hole, just an accepted interim state. Real enforcement is
planned as a separate service, not part of the proxy path.

---

## Services

Services are declared entirely through Docker container labels — see
[Public vs private](#public-vs-private) above. There is no manual/bare-metal
service path: every service needs a Docker container with a `caddy` label
for Caddy to route to. `zetafile.yml` is used only for firewall
configuration now — see [architecture.md](architecture.md) for its format.

### DNS naming

```
<service-name>.<agent-hostname>.mesh
      files    .    shire       .mesh
```

Resolves to the agent's mesh IP. Caddy reads the `Host` header (forwarded
unmodified by the agent's gateway) to determine which service to route the
request to.

### Access syntax

The `user:<hostname>` syntax refers to the mesh hostname of the peer device (the hostname it registered with, not its DNS FQDN). The controller resolves this to the device's current WireGuard public key at announcement time. **Not currently enforced** — see [ACL enforcement — not yet wired up](#acl-enforcement--not-yet-wired-up).

```yaml
zeta.access: user:graveyard,user:laptop
```

### Applying changes

Services are re-announced to the controller automatically whenever Docker container labels change (the agent watches Docker events). The controller resolves the updated access list and pushes a new NetworkMap to all peers. Changes take effect on connected agents within seconds — no restart or manual re-announce needed.

---

## Management API

The agent exposes a local HTTP API at `127.0.0.1:6080` (configurable via `ZETA_HTTP_ADDR`). This is the interface used by the local web UI embedded in the agent binary. It's read-only — services are declared via Docker labels, not through this API.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Node ID, hostname, mesh IP |
| `GET` | `/api/services` | List currently discovered private (mesh-only) services |

### `GET /api/status`

```json
{
  "node_id": "uuid",
  "hostname": "shire",
  "mesh_ip": "100.64.0.2"
}
```

### `GET /api/services`

```json
[
  {
    "name": "files",
    "access": ["user:graveyard"]
  }
]
```

---

## Docker

The agent needs `--privileged` and `--network host` — WireGuard requires kernel access and the mesh interface must be on the host network stack.

```bash
docker run -d \
  --name zeta-agent \
  --privileged \
  --network host \
  -v zeta-agent-state:/var/lib/zeta \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -e ZETA_COORDINATOR=<controller-ip>:50051 \
  ghcr.io/kreativethinker/zeta/agent:latest \
  --preauth-key <key>
```

Omit `--preauth-key` after the first run — the state volume persists the enrollment.

Minimum capabilities instead of `--privileged`:

```bash
--cap-add NET_ADMIN \
--cap-add SYS_MODULE \
--device /dev/net/tun
```

Note: `--network host` is required regardless of capability mode — Docker bridge networking breaks the iptables-based DNS interception used by `systemd-resolved` when the container is privileged.

---

## Verifying connectivity

```bash
# WireGuard interface and peers
sudo wg show zeta0

# Mesh IP assigned to interface
ip addr show zeta0

# Route is present
ip route | grep 100.64

# DNS resolves mesh names
dig @127.0.0.1 shire.mesh
dig @127.0.0.1 files.shire.mesh

# Ping a peer
ping 100.64.0.3

# Access a service directly (bypasses Caddy)
curl -H "Host: files.shire.mesh" http://100.64.0.3:1080/
```
