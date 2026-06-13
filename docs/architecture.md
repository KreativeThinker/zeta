# Architecture

## Overview

Zeta is a WireGuard-based mesh VPN with a central coordinator for key distribution and a fully peer-to-peer data plane. The coordinator is the single point of authority for device identity and network map distribution — but it is never in the path of actual traffic between peers.

```
┌──────────────────────────────────────┐
│             Controller               │
│                                      │
│  ┌──────────┐  ┌──────┐  ┌───────┐  │
│  │  SQLite  │  │  CA  │  │  API  │  │
│  └──────────┘  └──────┘  └───────┘  │
│                                      │
│  gRPC :50051        REST/UI :8080    │
└───────────┬──────────────────────────┘
            │ control plane (gRPC streams)
    ┌───────┴────────┐
    ▼                ▼
┌─────────┐     ┌─────────┐
│ Agent A │◄───►│ Agent B │   data plane (WireGuard, direct)
│ shire   │     │graveyard│   100.64.x.x/10
└─────────┘     └─────────┘
```

---

## Control plane vs data plane

### Control plane — hub-and-spoke

All coordination flows through the controller. Every agent holds a persistent gRPC bidirectional stream (`Sync`) to the controller. Over this stream:

- The controller pushes **NetworkMap** updates whenever any device joins, leaves, changes its external endpoint, or announces/removes services.
- The agent pushes **EndpointUpdate** messages every 30 seconds with its current STUN-discovered external IP:port, and **ServiceAnnounce** messages when its `zetafile.yml` changes.

The controller is the only component that writes to state (SQLite). Agents are stateless with respect to the mesh topology — they receive it, apply it, and discard it on restart (relying on the controller to push it again).

### Data plane — peer-to-peer

Actual traffic between devices travels through direct WireGuard tunnels, with no controller involvement. When an agent receives a NetworkMap update, it calls `wg set` (via the kernel WireGuard interface) to configure a peer entry for each device:

```
[Peer]
PublicKey = <peer-wg-pubkey>
AllowedIPs = <peer-mesh-ip>/32
Endpoint = <stun-discovered-ip>:<wg-port>
```

From that point, the kernel routes all `100.64.x.x` traffic through the `zeta0` interface, which WireGuard encrypts and sends directly to the peer's endpoint. The controller is not involved.

#### Controller offline impact

If the controller goes offline:
- All existing WireGuard tunnels remain functional. Peers continue communicating.
- Endpoint changes (e.g. a laptop moves to a new network) do not propagate — the peer list becomes stale.
- New enrollments are not possible.
- Service ACL updates do not propagate.

---

## Components

### Controller

Runs as a single binary (`zeta-controller`). Contains:

- **SQLite database** — source of truth for devices, preauth keys, services, and audit log.
- **ECDSA P-256 CA** — issues a device certificate to each enrolling agent. Auto-generated on first run and stored in the database.
- **gRPC server** (`:50051`) — handles enrollment (`Register`) and the ongoing `Sync` stream.
- **REST API + admin UI** (`:8080`) — SvelteKit SPA embedded in the binary, plus a REST API for managing devices, keys, and viewing services.

### Agent

Runs as a single binary (`zeta-agent`) on each device. Requires root (or `CAP_NET_ADMIN`). Contains:

- **WireGuard manager** — creates and configures the `zeta0` interface; applies peer configs from NetworkMap updates.
- **STUN client** — discovers the device's external IP:port via `stun.l.google.com:19302` using IPv4. Reports the result to the controller every 30 seconds.
- **DNS resolver** — in-process DNS server (default `127.0.0.1:53`) that answers `A` queries for `*.mesh` names. Mesh hostnames resolve to peer mesh IPs. Service names (`service.hostname.mesh`) also resolve to the host's mesh IP.
- **HTTP proxy** — single-port reverse proxy (default `0.0.0.0:1080`) that routes requests by `Host` header to the correct local backend. Enforces per-service ACLs using the WireGuard public key of the source IP. Supports traffic arriving directly from WireGuard peers or via a local reverse proxy (e.g. Caddy), reading `X-Forwarded-For` when the direct source is not a mesh IP.
- **gRPC sync client** — maintains the persistent `Sync` stream to the controller with exponential backoff reconnection.
- **Management UI** — lightweight HTTP API (default `127.0.0.1:6080`) for viewing status, managing the zetafile, and reading proxy access logs.

### zetafile

A YAML file on each agent (`zetafile.yml`) declaring which local services to expose to the mesh. See [agent.md](agent.md#zetafile) for the full reference.

---

## Enrollment flow

```
Agent                                   Controller
  │
  │── Register(wg_public_key,  ────────────────────►│
  │           preauth_key,                          │  1. Validate preauth key
  │           hostname, os,                         │  2. Mark key as used (if non-reusable)
  │           agent_version)                        │  3. Allocate next free 100.64.x.x IP
  │                                                 │  4. Issue ECDSA cert (signed by mesh CA)
  │                                                 │  5. Record device in DB
  │◄── NodeConfig(node_id, mesh_ip, ───────────────│
  │               domain, cert_pem,                │
  │               ca_pem, key_pem)                 │
  │                                                 │
  │  [Agent configures zeta0, saves state.json]     │
  │                                                 │
  │── OpenSync(node_id in header) ─────────────────►│
  │◄── NetworkMap(all current peers) ──────────────│
  │                                                 │
  │  [Agent applies WireGuard peer configs]         │
  │                                                 │
  │── EndpointUpdate(stun_ip:port) ────────────────►│  every 30 s
  │── ServiceAnnounce(services) ───────────────────►│  on connect + zetafile change
  │◄── NetworkMap(updated) ────────────────────────│  any time any peer changes
```

State saved in `state.json` after enrollment:
- WireGuard private/public key pair
- Assigned mesh IP
- Mesh domain
- Device certificate + private key (PEM)
- CA certificate (PEM)

On subsequent starts, the agent loads `state.json` and skips enrollment.

---

## Service announcement flow

```
Agent (shire)                   Controller                  Agent (graveyard)
  │                                  │                            │
  │── ServiceAnnounce ──────────────►│                            │
  │   [{name: "files",               │  1. Resolve "user:graveyard"
  │     target: "127.0.0.1:9010",    │     → graveyard's WG pubkey
  │     allowed: ["user:graveyard"]} │  2. DELETE + INSERT services
  │                                  │  3. NotifyAll()
  │◄── NetworkMap (updated) ─────────│──────────────────────────►│
  │    (self entry includes          │                            │
  │     services with ACL pubkeys)   │  graveyard now knows:      │
  │                                  │  - files.shire.mesh exists │
  │                                  │  - its allowed pubkeys     │
```

The controller resolves `user:<hostname>` access entries to WireGuard public keys at announcement time. This means access is bound to the key that was active when the service was announced — re-enrollment with a new key revokes access automatically.

---

## Mesh IP allocation

Zeta uses the CGNAT range `100.64.0.0/10` (RFC 6598, ~4 million addresses):

| Address | Purpose |
|---|---|
| `100.64.0.1` | Reserved for controller |
| `100.64.0.2` | First enrolled device |
| `100.64.0.3` | Second enrolled device |
| … | Sequential allocation |

The controller scans the `devices` table to find the next unallocated IP. Deleted devices do not free their IP (no reuse by default).

---

## DNS

Each agent runs an in-process DNS server. The system DNS resolver is configured via `systemd-resolved` to use it for the `mesh` zone only — all other queries fall through to the upstream resolver (`1.1.1.1:53` by default).

Registered names (updated on each NetworkMap push):

| Pattern | Resolves to |
|---|---|
| `<hostname>.mesh` | Peer's mesh IP |
| `<service>.<hostname>.mesh` | Peer's mesh IP (proxy routes by `Host` header) |

Only `A` (IPv4) records are served. `AAAA` queries fall through to upstream.

---

## Proxy and ACL enforcement

The agent proxy listens on a single TCP port (default `0.0.0.0:1080`). For each incoming HTTP request:

1. Extract the source IP from the TCP connection.
2. If the source is not in the mesh CIDR (`100.64.0.0/10`) — i.e. traffic arrived via a local reverse proxy — read the `X-Forwarded-For` header for the original mesh peer IP.
3. Map the mesh IP → WireGuard public key using the `ipToPK` table (populated from the NetworkMap).
4. Look up the service by the first label of the `Host` header.
5. Check whether the pubkey is in that service's `allowedPKs` set (populated from the NetworkMap's resolved ACL).
6. If allowed, reverse-proxy the request to the service's configured `target` address.

Access events (allowed and denied) are recorded in a ring buffer and exposed via the agent management API.

---

## Data flow summary

```
[graveyard browser]
      │  DNS: files.shire.mesh?
      ▼
[graveyard DNS resolver: 127.0.0.1:53]
      │  A → 100.64.0.3   (shire's mesh IP)
      ▼
[graveyard WireGuard: zeta0]
      │  encrypt, send to shire's STUN endpoint
      ▼
[shire WireGuard: zeta0]
      │  decrypt, deliver to 100.64.0.3:1080
      ▼
[shire proxy: 0.0.0.0:1080]
      │  Host: files.shire.mesh
      │  ACL: graveyard pubkey ∈ allowedPKs["files"]?
      ▼
[shire backend: 127.0.0.1:9010]
```
