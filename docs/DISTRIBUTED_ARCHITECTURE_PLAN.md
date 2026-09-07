# Zeta: Gossip-Based Decentralization Plan

## Context

The current Zeta architecture routes all control-plane state through a central coordinator: peer discovery, service ACL resolution, and NetworkMap fan-out all depend on the coordinator being online. Additionally, WireGuard's flat mesh (100.64.0.0/10 routed to all peers) allows any peer to make direct connections to any other peer's ports, bypassing the HTTP proxy ACL entirely.

This plan replaces the coordinator-driven architecture with a fully decentralized, gossip-based system. Key outcomes:
- No coordinator required at runtime; agents communicate peer-to-peer via SWIM gossip
- Per-agent nftables firewall blocks all direct inbound mesh traffic except whitelisted service/gossip ports
- Service metadata is encrypted with X25519 ECIES so only authorized peers can learn what services exist
- New peers join via an out-of-band join token (no central enrollment server)

---

## Target Architecture

```
agent A ──gossip──▶ agent B ──gossip──▶ agent C
         ◀──gossip──          ◀──gossip──

Each agent:
  - gossip engine (memberlist/SWIM) for peer/service discovery
  - nftables firewall (DROP by default, auto-whitelist from zetafile ACLs)
  - encrypted service announcements (X25519 ECIES per authorized peer)
  - HTTP proxy (unchanged ACL enforcement)
  - DNS resolver (populated from gossip instead of NetworkMap)
```

The `controller/` directory is preserved until Phase 6 (clean removal after all agents migrate).

---

## Phase 1: Peer Firewall

**Goal**: Close the direct-port-access gap. Default DROP all inbound on the mesh interface; auto-whitelist peers based on zetafile service ACL config.

### New: `agent/internal/firewall/nftables.go`

```go
type Manager struct {
    iface string
    conn  *nftables.Conn
    set   *nftables.Set  // "zeta-allowed" ipv4_addr set
}

func New(iface string) (*Manager, error)
func (m *Manager) Setup() error              // create table+chain+base rules
func (m *Manager) SyncAllowedIPs(ips []net.IP) error  // atomic set swap
func (m *Manager) Teardown() error
```

**nftables schema** (applied programmatically via `github.com/google/nftables`):

```
table inet zeta {
    set zeta-allowed { type ipv4_addr; }

    chain mesh-in {
        type filter hook input priority 0; policy drop;
        iifname "<iface>" udp dport <gossip_port> accept   # gossip always open
        iifname "<iface>" ip saddr @zeta-allowed accept    # ACL-whitelisted peers
        iifname "<iface>" drop
    }
}
```

The gossip port and proxy port are allowed unconditionally (needed before ACLs resolve). All other inbound on the mesh interface is dropped.

**What gets whitelisted**: union of all allowed peer mesh IPs across all local services in zetafile, resolved from gossip state. Rebuilt whenever gossip state changes.

**Config addition** in `agent/internal/config/config.go`:
```go
type FirewallConfig struct {
    Enabled    bool `yaml:"enabled"`     // default true
    GossipPort int  `yaml:"gossip_port"` // default 7946
}
```

**Integration point**: Instantiate `firewall.Manager` in `main.go` after `wgMgr.EnsureInterface()`. Call `fwMgr.Setup()`. Pass it to the new gossip loop. `defer fwMgr.Teardown()` on shutdown.

**Library**: `github.com/google/nftables` — pure Go, no exec subprocess.

---

## Phase 2: Gossip Engine

**Goal**: Replace the gRPC coordinator stream with SWIM gossip. Each agent gossips a `PeerRecord`; the union of all gossip state replaces the coordinator's NetworkMap.

### New package: `agent/internal/gossip/`

| File | Purpose |
|------|---------|
| `gossip.go` | `Engine` type: memberlist lifecycle, `Join`, `Snapshot`, `OnChange`, `AnnounceServices` |
| `delegate.go` | `memberlist.Delegate` + `EventDelegate` implementation |
| `state.go` | `NodeMeta`, `PeerRecord`, `ServiceEnvelope` types + protobuf codec |
| `token.go` | `JoinToken` parse/generate, `SeedPeer` type |

### Gossip Message Schema

**NodeMeta** (≤512 bytes, carried in every memberlist heartbeat):
```go
type NodeMeta struct {
    Version    uint8   // schema version = 1
    WGPubKey   []byte  // 32 bytes raw Curve25519
    MeshIP     string  // "100.64.x.x"
    WGEndpoint string  // "ip:port" from STUN
    Hostname   string
    PSKHash    []byte  // HMAC-SHA256(PSK, WGPubKey)[:8] — membership gate
}
```

**PeerRecord** (full state, exchanged via TCP PushPull on join + periodic sync):
```go
type PeerRecord struct {
    Version     uint8
    WGPubKey    []byte
    MeshIP      string
    Hostname    string
    Endpoint    string
    UpdatedAt   int64            // Unix seconds, last-write-wins
    EncServices []ServiceEnvelope // see Phase 3
}
```

### Engine API
```go
func New(cfg Config) (*Engine, error)
func (e *Engine) Join(ctx context.Context, seeds []SeedPeer) error
func (e *Engine) Snapshot() []PeerRecord
func (e *Engine) OnChange() <-chan struct{}  // closed+replaced on any state change
func (e *Engine) AnnounceServices(svcs []EncServiceSpec) error
func (e *Engine) ResolveHostname(hostname string) (pubkey []byte, ok bool)
```

### Gossip Security

memberlist's built-in `SecretKey` field encrypts all gossip traffic with AES-128-GCM. The key is derived as `HKDF-SHA256(PSK, "zeta-gossip-encrypt", 16)`. Only nodes holding the network PSK can participate. The `PSKHash` in `NodeMeta` provides a lightweight membership check before accepting peers into the WG config.

### Delegate wiring

- `NodeMeta()` → encode this node's `NodeMeta`
- `LocalState(join bool)` → protobuf-encode this node's full `PeerRecord`
- `MergeRemoteState(buf, join)` → decode remote `PeerRecord`, update peers map with LWW on `UpdatedAt`, signal `OnChange`
- `NotifyJoin/Leave/Update` → update peers map, signal `OnChange`

---

## Phase 3: Encrypted Service Announcements

**Goal**: Encrypt service metadata so unauthorized peers cannot learn service names, targets, or access lists from gossip.

### New: `agent/internal/crypto/ecies.go`

**Encryption** (1 call per service announcement, repeated per authorized peer):

```go
func EncryptServiceAnnouncement(
    payload ServicePayload,         // {Name, TargetAddr, AllowedPKs}
    authorizedPKs [][]byte,        // raw 32-byte Curve25519 pubkeys
) (ServiceEnvelope, error)
```

1. Serialize `ServicePayload` to protobuf bytes.
2. Generate random 32-byte AES body key + 12-byte nonce; AES-256-GCM encrypt.
3. For each `authorizedPK`:
   - Generate fresh ephemeral Curve25519 scalar.
   - `shared = X25519(ephemeral_scalar, authorizedPK)`.
   - `wrapKey = HKDF-SHA256(shared, "zeta-service-key", 32)`.
   - AES-256-GCM encrypt body key with wrapKey → `ECIESCiphertext`.
4. Return `ServiceEnvelope{Recipients: []ECIESCiphertext, IV, Body}`.

**Decryption** (each peer tries its own privkey against all envelopes it receives):

```go
func DecryptServiceAnnouncement(
    env ServiceEnvelope,
    recipientPrivKey []byte,  // local WG private key (Curve25519 scalar)
    recipientPubKey  []byte,
) (*ServicePayload, error)
```

Finds matching `ECIESCiphertext` by `RecipientPK`, reverses the DH to recover body key, decrypts `Body`.

**Wire types**:
```go
type ServiceEnvelope struct {
    Recipients []ECIESCiphertext
    IV         []byte  // 12 bytes
    Body       []byte  // AES-256-GCM ciphertext of protobuf ServicePayload
}

type ECIESCiphertext struct {
    RecipientPK []byte  // 32 bytes — identifies recipient
    EphemeralPK []byte  // 32 bytes — sender's ephemeral pubkey
    EncKey      []byte  // 48 bytes: encrypted body key + GCM tag
    KeyNonce    []byte  // 12 bytes
}

type ServicePayload struct {
    Name       string
    TargetAddr string
    AllowedPKs [][]byte  // raw 32-byte pubkeys — recipient uses to build firewall allowlist
}
```

**Libraries**: `golang.org/x/crypto/curve25519`, `golang.org/x/crypto/hkdf`, `crypto/aes`, `crypto/cipher` — all already transitive deps via WireGuard.

**Hostname resolution without coordinator**: Zetafile uses `access: ["user:graveyard"]`. The agent resolves `hostname → WGPubKey` via `engine.ResolveHostname()`, which is populated from gossip `NodeMeta.Hostname`. Service re-encryption triggers on each gossip change event when new hostnames become resolvable.

---

## Phase 4: Join Token + Bootstrap Flow

**Goal**: New peer joins the network without a coordinator.

### Join token format

```
zeta-join:<base64url(JoinTokenPayload protobuf)>
```

```go
type JoinTokenPayload struct {
    NetworkID string     // human name, e.g. "myzeta"
    PSK       []byte     // 32-byte random network PSK
    Domain    string     // mesh domain, e.g. "mesh"
    Seeds     []SeedPeer // 1..N seed peers
}

type SeedPeer struct {
    Addr     string  // "ip:port"
    WGPubKey []byte  // 32 bytes — needed to configure WG peer before gossip
}
```

### New subcommand: `zeta token create`

Reads local `state.json`, constructs `JoinTokenPayload` with self as seed, prints `zeta-join:...` to stdout.

### Bootstrap flow (new node)

```
$ zeta agent --join-token zeta-join:AABBCC...
```

1. Parse token → extract PSK, domain, seeds.
2. Derive mesh IP from WG pubkey (Phase 5).
3. Save state (`psk`, `domain`, `network_id`, `last_seeds`).
4. Configure WG interface with derived mesh IP.
5. Configure WG peers for seed nodes (from token `Seeds`).
6. Start gossip engine with PSK-derived encryption key.
7. `engine.Join(ctx, seeds)` — blocks until ≥1 seed responds.
8. First PushPull delivers full peer list → `engine.Snapshot()` returns all peers.
9. Apply WG peers, DNS, proxy, firewall from snapshot.
10. Encrypt own services for resolved authorized peers.

### State file migration

**Remove**: `NodeID`, `CertPEM`, `CertKeyPEM`, `CAPEM`  
**Add**: `PSK []byte`, `NetworkID string`, `IPSalt uint8`, `LastSeeds []SeedPeer`

```go
type State struct {
    WGPrivateKey string     `json:"wg_private_key"`
    WGPublicKey  string     `json:"wg_public_key"`
    MeshIP       string     `json:"mesh_ip"`       // derived+cached
    Hostname     string     `json:"hostname"`
    PSK          []byte     `json:"psk"`
    Domain       string     `json:"domain"`
    NetworkID    string     `json:"network_id"`
    IPSalt       uint8      `json:"ip_salt"`       // 0 = no collision; increments on collision
    LastSeeds    []SeedPeer `json:"last_seeds"`
}
```

`state.Load` detects absence of `psk` and prompts: "not enrolled — run with --join-token". WG keys are preserved across migration.

---

## Phase 5: Decentralized IP Allocation

**Goal**: Derive mesh IP deterministically from WG public key; no central allocator.

### Hash formula

```go
func DeriveIPFromPubKey(pubKey []byte, salt uint8) net.IP {
    h := sha256.Sum256(append(pubKey, salt))
    // 100.64.0.0/10 → 2^22 host addresses
    offset := binary.BigEndian.Uint32(h[:4]) & 0x003FFFFF
    if offset < 2 { offset += 2 }  // skip .0 and .1
    base := uint32(0x64400000)      // 100.64.0.0
    ip := base + offset
    return net.IP{byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip)}
}
```

**Collision detection**: On receiving a gossip `NodeMeta` where `MeshIP == self.MeshIP` but `WGPubKey != self.WGPubKey`, the node whose pubkey sorts lexicographically higher re-derives with `IPSalt++`, reconfigures WG, updates gossip NodeMeta, and saves the new salt to state.

**New file**: `agent/internal/crypto/ip.go` — `DeriveIPFromPubKey`, `DetectCollision`.

---

## Phase 6: Coordinator Removal

**Goal**: Remove the gRPC control client from the agent entirely.

### Replace `runSyncLoop` with `runGossipLoop`

```go
func runGossipLoop(
    ctx context.Context,
    engine *gossip.Engine,
    wgMgr *wg.Manager,
    resolver *dns.Resolver,
    proxyMgr *proxy.Manager,
    fwMgr *firewall.Manager,
    st *state.State,
    zf *config.Zetafile,
    cfg *config.Config,
)
```

Loop body (triggers on `engine.OnChange()`):
1. `snap := engine.Snapshot()` — get current peer list.
2. Build `[]wg.PeerConfig` from snap; call `wgMgr.ApplyPeers(peers)`.
3. Call `resolver.UpdateFromGossip(snap, st.Domain)`.
4. For each local service: resolve `access:` hostnames via `engine.ResolveHostname`; build `[]proxy.ServiceConfig`; call `proxyMgr.Sync(svcs, ipToPK, ipToHost)`.
5. Collect union of allowed peer IPs across all local services; call `fwMgr.SyncAllowedIPs(ips)`.
6. Re-encrypt own services for all newly-resolved authorized pubkeys; call `engine.AnnounceServices(...)`.

STUN discovery runs independently on a 30s ticker, calling `engine.UpdateEndpoint(endpoint)` which re-gossips a compact `MsgEndpointHint` broadcast.

### DNS resolver update

Replace `UpdateFromNetworkMap(peers []*zetapb.Peer, domain string)` with:
```go
func (r *Resolver) UpdateFromGossip(peers []gossip.PeerRecord, domain string)
```
Same internal logic, different input type.

### Packages to delete
- `agent/internal/control/` — entire package
- `zetapb` imports from agent (proto package no longer needed by agent)
- `--preauth-key` and `--coordinator` flags from `main.go`
- `CoordinatorConfig` from `agent/internal/config/config.go`

### Flag changes
- Remove: `--preauth-key`, `--coordinator`
- Add: `--join-token` (first run only; subsequent runs use state file)

---

## New Files / Packages Summary

| Path | Purpose |
|------|---------|
| `agent/internal/firewall/nftables.go` | nftables Manager: Setup, SyncAllowedIPs, Teardown |
| `agent/internal/gossip/gossip.go` | Engine: memberlist lifecycle, OnChange, Snapshot, AnnounceServices |
| `agent/internal/gossip/delegate.go` | memberlist.Delegate + EventDelegate |
| `agent/internal/gossip/state.go` | PeerRecord, NodeMeta, ServiceEnvelope + codec |
| `agent/internal/gossip/token.go` | JoinToken parse/generate |
| `agent/internal/crypto/ecies.go` | EncryptServiceAnnouncement, DecryptServiceAnnouncement |
| `agent/internal/crypto/ip.go` | DeriveIPFromPubKey, DetectCollision |

## Modified Files

| Path | Change |
|------|--------|
| `agent/cmd/agent/main.go` | Replace runSyncLoop→runGossipLoop; add firewall+gossip init; swap --preauth-key/--coordinator for --join-token |
| `agent/internal/state/state.go` | Remove NodeID/cert fields; add PSK/NetworkID/IPSalt/LastSeeds |
| `agent/internal/config/config.go` | Remove CoordinatorConfig; add GossipConfig, FirewallConfig |
| `agent/internal/dns/resolver.go` | UpdateFromNetworkMap → UpdateFromGossip([]gossip.PeerRecord) |
| `agent/go.mod` | Add hashicorp/memberlist, google/nftables |

## New Dependencies

```
github.com/hashicorp/memberlist  — SWIM gossip protocol
github.com/google/nftables       — pure-Go nftables management
```
(x/crypto/curve25519, x/crypto/hkdf already present as transitive WG deps)

---

## Key Design Decisions

**SSS with k=1 = ECIES per recipient**: Shamir's at threshold k=1 is mathematically equivalent to giving each peer their own copy of the secret. We implement this as per-recipient ECIES wraps, which achieves the stated property ("any 1 authorized peer can decrypt") and gives a clean path to upgrade to k>1 threshold in future by replacing ECIES wraps with actual Shamir shares.

**WG keys reused for X25519 ECIES**: WireGuard uses Curve25519 (same curve as X25519). The 32-byte raw WG public key decodes directly for use in ECDH — no format conversion needed beyond base64-decoding the WG key string.

**Gossip layer encryption**: memberlist's built-in `SecretKey` (AES-128-GCM) encrypts all gossip traffic. Only nodes that know the PSK can join the gossip cluster. This is separate from the service announcement encryption.

**Proxy unchanged**: `proxy.Manager.Sync(services, ipToPK, ipToHost)` is called identically; only the source of that data changes (gossip instead of coordinator NetworkMap). Zero changes to the ACL enforcement logic.

---

## Verification

### Phase 1 (Firewall)
```bash
# On peer A after deployment:
nft list table inet zeta
# Verify chain with DROP default and gossip/proxy allows

# From peer B (unauthorized):
ssh <peer-A-mesh-IP>  # should hang/reject (not bypass proxy)

# From peer B (authorized for a service):
curl http://<service>.mesh  # should work via proxy
ssh <peer-A-mesh-IP>  # should still fail (SSH not in whitelist)
```

### Phase 2 (Gossip)
```bash
# Start two agents with same join token
# Check gossip connectivity:
zeta status  # should show both peers

# Kill coordinator — agents should retain WG connectivity and DNS
# Bring up a third peer via join token — should appear in other peers' DNS within ~5s
```

### Phase 3 (Encrypted service discovery)
```bash
# Capture gossip traffic with tcpdump on mesh interface
# Verify service names are not visible in cleartext
# Authorized peer: curl http://service.mesh → 200
# Unauthorized peer: DNS should not resolve service; proxy should 403
```

### Phase 4-6 (Full decentralization)
```bash
# Shut down controller entirely
# All three agents should maintain:
#   - WG connectivity
#   - DNS resolution of peer hostnames
#   - Proxy ACL enforcement
#   - Correct firewall rules

# New peer joins with join token (no controller needed):
zeta agent --join-token zeta-join:...
# Should appear in DNS on existing peers within ~5s (one gossip round)
```
