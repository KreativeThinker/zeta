# Security model

## What Zeta provides

Zeta implements **Zero Trust Network Access (ZTNA)** at the device and network layer. The core principle: network location is not trusted. A device on the mesh must prove its identity cryptographically for every tunnel it establishes, and access to services is granted explicitly per-device.

### Cryptographic identity

Every device's identity is its **WireGuard public key** (Curve25519). Keys are generated on the device at enrollment time and never leave it (the private key is stored only in `state.json` on the device itself). The controller never sees a device's private key.

IP addresses on the mesh are untrusted as identifiers. The source IP of a connection is used only to look up the corresponding WireGuard public key — the key is the identity.

### Mutual authentication

WireGuard authenticates both sides of every tunnel using public-key cryptography. A device cannot join the mesh without a key pair registered with the controller. An unknown or deregistered device cannot establish a WireGuard session with any peer.

### Device certificates

At enrollment, the controller's ECDSA P-256 CA issues a device certificate. The certificate binds the device's WireGuard public key to its mesh identity. The CA root is distributed to all agents and can be used to verify peer certificates in the future (e.g. for mTLS between services).

### Per-service ACL

Service access is controlled at the service level via `zetafile.yml`. Each service declares an explicit access list (`user:<hostname>`), which the controller resolves to WireGuard public keys. The agent proxy enforces this on every connection — a peer not in the access list receives `403 Forbidden` even if it has a valid WireGuard tunnel.

If the access list is empty, no peer can connect to the service.

### Least privilege

- Devices only receive NetworkMap entries for peers — they do not receive each other's private keys or certificates.
- Service access must be explicitly granted; there is no implicit "all mesh peers" access.
- Preauth keys can be single-use and time-limited.

---

## Threat model

### What is protected

| Threat | Mitigation |
|---|---|
| Unenrolled device joining the mesh | Preauth key required; WireGuard rejects unknown public keys |
| Traffic interception between peers | WireGuard ChaCha20-Poly1305 encryption on all peer-to-peer traffic |
| Unauthorized access to a mesh service | Per-service ACL enforced by the agent proxy |
| Spoofed mesh IP | WireGuard validates the public key behind every packet; IP is derived from the key |
| Stale key after re-enrollment | ACL is stored as pubkeys; old pubkey loses access automatically on re-announcement |
| Unauthorized enrollment | Preauth key must be valid, unexpired, and (if non-reusable) unused |

### What is not protected

| Limitation | Notes |
|---|---|
| **Device-level identity only** | Authentication is per-device, not per-user. A compromised device grants full access to its allowed services for whoever controls it. |
| **No device posture checks** | The controller does not verify that a device is patched, encrypted, or otherwise healthy before granting network access. |
| **Controller is a single point of trust** | The controller CA is the root of trust. A compromised controller can issue fraudulent certificates and push malicious NetworkMaps. |
| **Relay fallback is unauthenticated at the transport layer** | Agents fall back to relaying WireGuard traffic through the controller's `Relay` gRPC stream when a direct handshake doesn't succeed within ~20s (see architecture doc). The controller only forwards opaque, already-encrypted WireGuard packets by node ID and never decrypts them, but — like the `Sync` stream today — the stream itself isn't yet authenticated beyond a self-reported `node-id` header; a compromised agent could in principle claim another node's ID and intercept its relayed traffic. This closes once mTLS with the CA-issued device certs lands on the gRPC server (tracked separately). |
| **WireGuard sessions outlive ACL changes** | Once a WireGuard tunnel is up, it stays up until it times out or the peer list is updated. An ACL change propagates within seconds (next NetworkMap push), but there is a brief window. |
| **Proxy ACL is HTTP-layer only** | The single-port proxy enforces ACL at the HTTP layer. Non-HTTP TCP services require separate handling or firewall rules. |

---

## Key issuance and the CA

The controller auto-generates an ECDSA P-256 CA on first run and stores it in the database. Alternatively, you can supply your own CA:

```yaml
ca:
  cert_file: /etc/zeta/ca.crt
  key_file: /etc/zeta/ca.key
```

The CA is used to:
1. Sign device certificates at enrollment (`cert_pem`, `key_pem` in `NodeConfig`)
2. Distribute the root to agents (`ca_pem`) so they can verify the controller and peer certs

The CA root should be treated as a secret. Anyone who can sign with it can issue valid device certificates.

---

## Access revocation

### Revoking a device

Delete the device via `DELETE /api/v1/devices/{id}`. This:
1. Removes the device from the database
2. Triggers `NotifyAll` — every connected agent receives an updated NetworkMap without the deleted device's peer entry
3. WireGuard peers are updated on each agent; the deleted device can no longer establish tunnels to any peer

Note: the deleted device's existing WireGuard sessions with other peers will close when WireGuard's handshake timer expires (typically within 3 minutes), or immediately when the peer list is applied on the remote agent.

### Revoking service access

Update the service's `zetafile.yml` to remove the peer from the access list, then save via `POST /api/services`. The controller resolves the new access list and pushes an updated NetworkMap. The agent proxy updates its ACL within seconds.

### Revoking a preauth key

Delete the key via `DELETE /api/v1/preauth-keys/{key}`. Keys that have already been used for enrollment do not affect enrolled devices — the device is already in the DB. Deletion only prevents future use of that token.

---

## Network isolation

Zeta uses the **CGNAT range `100.64.0.0/10`** (RFC 6598) for mesh IPs. This range:
- Is not routed on the public internet
- Is unlikely to conflict with home or office LAN ranges (unlike `10.x.x.x` or `192.168.x.x`)
- Provides ~4 million addresses

Agents add a `100.64.0.0/10` route via `zeta0`. Traffic to mesh IPs goes through WireGuard; all other traffic uses the default route unchanged. The mesh is an overlay — it does not replace or interfere with the device's existing internet connectivity.

---

## Comparison to full zero trust

Zeta implements ZTNA at the **network and device layer**, which is what most commercial ZTNA products (Tailscale, Cloudflare Access in tunnel mode, Twingate) provide. It does not implement the full NIST SP 800-207 zero trust model, which additionally requires:

- **User identity** — authentication per-user, not per-device
- **Device posture** — health checks (patch level, disk encryption, MDM enrollment) before granting access
- **Continuous verification** — re-evaluating trust on every request, not just at session establishment
- **Request-level authorization** — "can this user perform this action on this resource" rather than "can this device reach this service"

For a self-hosted mesh VPN, the device-level model is a reasonable and strong security posture. Adding user identity would require an identity provider integration (e.g. OIDC); adding device posture would require a check-in agent and policy engine.
