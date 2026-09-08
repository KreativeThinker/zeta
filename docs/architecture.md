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
- **Proxy gateway** — single-port pass-through (default `0.0.0.0:1080`) that forwards every request, `Host` header unmodified, to a local Caddy instance (`lucaslorentz/caddy-docker-proxy`). Caddy is the only actual reverse proxy — it reads container labels off the Docker socket directly and does all HTTP routing, for both public and private services. See [agent.md](agent.md#proxy-gateway).
- **gRPC sync client** — maintains the persistent `Sync` stream to the controller with exponential backoff reconnection.
- **Management UI** — lightweight, read-only HTTP API (default `127.0.0.1:6080`) for viewing status and currently discovered services.

### zetafile

A YAML file on each agent (`zetafile.yml`) declaring firewall rules. Services are no longer declared here — they're discovered from Docker container `caddy`/`zeta.*` labels. See [agent.md](agent.md#proxy-gateway).

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
  │     allowed: ["user:graveyard"]} │     → graveyard's WG pubkey
  │                                  │  2. DELETE + INSERT services
  │                                  │  3. NotifyAll()
  │◄── NetworkMap (updated) ─────────│──────────────────────────►│
  │    (self entry includes          │                            │
  │     services with ACL pubkeys)   │  graveyard now knows:      │
  │                                  │  - files.shire.mesh exists │
  │                                  │  - its allowed pubkeys     │
```

The controller resolves `user:<hostname>` access entries to WireGuard public keys at announcement time. This means access is bound to the key that was active when the service was announced — re-enrollment with a new key revokes access automatically. **Not currently enforced against traffic** — see the note in [agent.md](agent.md#acl-enforcement--not-yet-wired-up).

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

## Proxy gateway

The agent's proxy listens on a single TCP port (default `0.0.0.0:1080`) and is a pure pass-through: every request is forwarded, `Host` header unmodified, to a local Caddy instance (`lucaslorentz/caddy-docker-proxy`, default `127.0.0.1:8888`). Caddy does the actual per-service routing, matching the request's `Host` header against each container's `caddy` label.

Public services (`zeta.public: "true"` container label) bypass this gateway entirely — Caddy binds them straight to `0.0.0.0:443`/`:80` with automatic HTTPS.

No ACL is enforced in this path today — see the note in [agent.md](agent.md#acl-enforcement--not-yet-wired-up).

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
[shire proxy gateway: 0.0.0.0:1080]
      │  Host: files.shire.mesh  (passed through unmodified)
      ▼
[shire Caddy: 127.0.0.1:8888]
      │  caddy label match: files.shire.mesh
      ▼
[shire backend: 127.0.0.1:9010]
```

---

## Android client

The Android client participates in the mesh as a full peer. It uses a different DNS delivery mechanism than the Linux agent — Android non-root apps cannot bind port 53, so a split DNS server inside the app is not viable.

### Architecture

```
ZetaVpnService  (android.net.VpnService)
  │
  │  VpnService.Builder
  │    .addAddress(<mesh_ip>, 10)
  │    .addRoute("100.64.0.0", 10)   ← only mesh CIDR goes through TUN
  │    .addDnsServer("100.64.0.1")   ← virtual DNS IP, no real host
  │    .establish()  →  TUN fd
  │
  │  fd.detachFd()  →  passed to Go library
  │
  ▼
┌──────────────────────────────────────────────────┐
│  libzetavpn.so  (vpnlib Go module, gomobile AAR)  │
│                                                   │
│  androidTUN  (plain read/write on fd, no ioctl)   │
│       │                                           │
│  FilteredTUN.Read()                               │
│  ├─ UDP dst=100.64.0.1:53?                        │
│  │   YES → DNSHandler                             │
│  │         ├─ *.mesh → sync.Map lookup            │
│  │         │           → synthesise A reply       │
│  │         │           → write reply to TUN       │
│  │         └─ other  → forward to upstream DNS    │
│  │                     → write reply to TUN       │
│  └─ NO  → pass to wireguard-go device.Device      │
│                                                   │
│  device.NewDevice(FilteredTUN, ...)               │
└──────────────────────────────────────────────────┘
```

### Why TUN-level DNS interception

`VpnService.Builder.addDnsServer("100.64.0.1")` tells Android to route all DNS queries from all apps to port 53 on the virtual IP `100.64.0.1`. A non-root app cannot bind port 53 directly. Instead, the Go library reads raw IP packets from the TUN fd: DNS queries appear as UDP packets with destination `100.64.0.1:53`. The library intercepts, answers, and writes the reply packet directly back into the TUN — bypassing wireguard-go entirely for DNS traffic.

### Split tunnel

Only the mesh CIDR (`100.64.0.0/10`) is routed through the VPN TUN. WireGuard peer endpoints and the upstream DNS server are on public IPs outside this range, so they route via the physical interface without looping through the TUN. `protect()` is not needed.

### TUNGETIFF constraint

Android VPN fds do not support the `TUNGETIFF` ioctl (requires `CAP_NET_ADMIN`). wireguard-go's `tun.CreateTUNFromFile` calls this ioctl to read the interface name and fails with `EPERM`. The vpnlib uses a custom `androidTUN` implementation that delegates only to `read()`/`write()` system calls and returns a static name — no ioctl involved.

### DNS record lifecycle

1. `ZetaVpnService.startVpn()` captures the system upstream DNS before `establish()` (once `100.64.0.1` is active as a DNS server, the system DNS changes).
2. On each `NetworkMap` push, `buildDnsJson()` serialises all peer hostnames and service names to a JSON map and calls `Vpnlib.setDNSRecords()`.
3. The Go layer atomically replaces its `sync.Map` with the new records.
4. When the VPN tears down, `100.64.0.1` is removed from Android's DNS configuration and the system reverts to its default resolver.
