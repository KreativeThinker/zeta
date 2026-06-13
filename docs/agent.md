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

## HTTP proxy

The agent proxy listens on a single TCP port (default `0.0.0.0:1080`) and routes HTTP requests by the `Host` header to the correct local backend. All services on a device share this one listener.

### Routing

The proxy extracts the service name from the first label of the `Host` header:

```
Host: files.shire.mesh  →  service name: "files"
```

It looks up the service's target address and reverse-proxies the request there.

### ACL enforcement

Before proxying, the request's source is identified:

1. If the TCP source IP is in the mesh CIDR (`100.64.0.0/10`), it is a direct WireGuard connection — use that IP.
2. If the TCP source IP is outside the mesh CIDR (e.g. a local reverse proxy like Caddy), read `X-Forwarded-For` for the original peer IP.

The IP is mapped to a WireGuard public key using the NetworkMap's `ipToPK` table. If the pubkey is not in the service's `allowedPKs` set, the connection is rejected with `403 Forbidden`.

Access events (time, service, source IP, hostname, allowed/denied) are logged in a ring buffer and readable via the management API.

### Caddy integration

If Caddy runs on the same host on ports 80/443, you can route mesh traffic through it rather than exposing port 1080 directly:

```
*.shire.mesh {
    tls internal
    reverse_proxy host.docker.internal:1080
}
```

Caddy sets `X-Forwarded-For` automatically. The agent proxy reads it and performs the ACL check against the original peer's mesh IP.

Add to your Caddy Docker service to make `host.docker.internal` resolve:

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

---

## zetafile

`zetafile.yml` declares which local services this agent exposes to the mesh. The agent watches for changes via the management API and re-announces to the controller whenever the file is updated.

### Format

```yaml
services:
  - name: files
    target: 127.0.0.1:9010
    access:
      - user:graveyard
      - user:laptop
```

### Fields

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Service identifier. Becomes the first DNS label: `<name>.<hostname>.mesh` |
| `target` | Yes | Local address to proxy to. Can be any `host:port` reachable from the agent process |
| `access` | No | List of mesh hostnames allowed to connect (`user:<hostname>`). Empty = no access |

### DNS naming

```
<service-name>.<agent-hostname>.mesh
      files    .    shire       .mesh
```

Resolves to the agent's mesh IP. The proxy reads the `Host` header to determine which service to route the request to.

### Access syntax

The `user:<hostname>` syntax refers to the mesh hostname of the peer device (the hostname it registered with, not its DNS FQDN). The controller resolves this to the device's current WireGuard public key at announcement time.

```yaml
access:
  - user:graveyard    # device with hostname "graveyard"
  - user:laptop       # device with hostname "laptop"
```

If a device re-enrolls (generating a new WireGuard key), services announced before the re-enrollment will deny the newly-keyed device until the service is re-announced.

### Applying changes

The zetafile is re-announced to the controller via the management API (`POST /api/services`). The controller resolves the updated access list and pushes a new NetworkMap to all peers. Changes take effect on connected agents within seconds.

---

## Management API

The agent exposes a local HTTP API at `127.0.0.1:6080` (configurable via `ZETA_HTTP_ADDR`). This is the interface used by the local web UI embedded in the agent binary.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Node ID, hostname, mesh IP |
| `GET` | `/api/services` | List services from zetafile |
| `POST` | `/api/services` | Add or update a service |
| `DELETE` | `/api/services/{name}` | Remove a service |
| `GET` | `/api/logs` | Last 200 proxy access events |

### `GET /api/status`

```json
{
  "node_id": "uuid",
  "hostname": "shire",
  "mesh_ip": "100.64.0.2"
}
```

### `POST /api/services`

```json
{
  "name": "files",
  "target": "127.0.0.1:9010",
  "access": ["user:graveyard"]
}
```

Saves to `zetafile.yml` and re-announces to the controller.

### `GET /api/logs`

Returns up to 200 most recent proxy access events, newest first.

```json
[
  {
    "time": "2026-06-13T10:00:00Z",
    "service": "files",
    "source_ip": "100.64.0.3",
    "hostname": "graveyard",
    "allowed": true
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
  -v ./zetafile.yml:/etc/zeta/zetafile.yml \
  -e ZETA_COORDINATOR=<controller-ip>:50051 \
  ghcr.io/kreativethinker/zeta/agent:latest \
  --zetafile /etc/zeta/zetafile.yml \
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
