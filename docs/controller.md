# Controller

The controller is the central coordination server. It handles enrollment, issues device certificates, maintains the network map, and pushes updates to all connected agents. It does not carry data-plane traffic.

---

## Running

```bash
# Minimal — all defaults, SQLite in ./zeta.db
./zeta-controller

# With a config file
./zeta-controller --config /etc/zeta/zeta.yaml

# Docker
docker run -d \
  --name zeta-controller \
  -p 8080:8080 \
  -p 50051:50051 \
  -v zeta-data:/data \
  -e ZETA_DB_PATH=/data/zeta.db \
  ghcr.io/kreativethinker/zeta/controller:latest
```

---

## Configuration

### Config file (`zeta.yaml`)

All fields are optional; defaults shown.

```yaml
db:
  path: zeta.db             # SQLite file path

grpc:
  addr: ":50051"            # gRPC listen (enrollment + sync)

http:
  addr: ":8080"             # REST API + admin UI

ca:
  cert_file: ""             # leave empty → CA stored in DB (auto-generated)
  key_file: ""

mesh:
  domain: mesh              # DNS zone: <hostname>.mesh
  cidr: "100.64.0.0/10"     # mesh IP pool
  controller_ip: "100.64.0.1"
```

### Environment variables

Environment variables override config file values.

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

---

## Database schema

SQLite file at `ZETA_DB_PATH`. Tables:

**`devices`** — one row per enrolled device.

| Column | Type | Description |
|---|---|---|
| `id` | TEXT PK | UUID |
| `hostname` | TEXT UNIQUE | Agent hostname at enrollment time |
| `os` | TEXT | `linux` / `android` |
| `wg_public_key` | TEXT UNIQUE | Base64 Curve25519 pubkey |
| `mesh_ip` | TEXT UNIQUE | Allocated `100.64.x.x` address |
| `cert_pem` | TEXT | Device certificate (ECDSA P-256) |
| `last_seen` | DATETIME | Last `Sync` stream activity |
| `last_endpoint` | TEXT | Last STUN-reported `ip:port` |
| `agent_version` | TEXT | Reported by agent at registration |
| `created_at` | DATETIME | Enrollment time |

**`preauth_keys`** — enrollment tokens.

| Column | Type | Description |
|---|---|---|
| `key` | TEXT PK | Random hex token |
| `label` | TEXT | Human-readable label |
| `reusable` | BOOLEAN | If false, consumed on first use |
| `expiry` | DATETIME | Token expires at this time |
| `used_at` | DATETIME | Set on first use (NULL if unused) |
| `created_at` | DATETIME | |

**`services`** — services announced by agents via `zetafile.yml`.

| Column | Type | Description |
|---|---|---|
| `id` | TEXT PK | UUID |
| `device_id` | TEXT FK → devices | Owning device |
| `name` | TEXT | Service name (unique per device) |
| `target_addr` | TEXT | Local proxy target (informational) |

**`service_access`** — per-service ACL, stored as resolved WireGuard pubkeys.

| Column | Description |
|---|---|
| `service_id` | FK → services |
| `allowed_pubkey` | WireGuard public key of the permitted peer |

Access is resolved at announcement time from `user:<hostname>` → current pubkey. If a device re-enrolls with a new key, its old pubkey entries do not grant access to services that re-announced after the re-enrollment.

**`audit_log`** — append-only log of significant events.

| Column | Description |
|---|---|
| `event` | Event name (e.g. `device.enrolled`, `device.deleted`) |
| `device_id` | Associated device (nullable) |
| `metadata` | JSON blob |
| `created_at` | |

---

## REST API

Base URL: `http://<controller>:8080/api/v1`

Protected endpoints require a session cookie obtained via `POST /auth/login`. The session is valid for 7 days. See [bruno/](bruno/) for a ready-to-use API collection.

### Authentication

#### `POST /auth/login`

```json
{ "password": "your-admin-password" }
```

Sets a `zeta_session` HTTP-only cookie on success.

```json
{ "status": "ok" }
```

#### `POST /auth/logout`

Revokes the current session. Returns `{ "status": "ok" }`.

---

### Status

#### `GET /status` — public, no auth required

```json
{
  "version": "1.0.0",
  "commit": "abc1234",
  "uptime": "3h14m",
  "device_count": 4
}
```

---

### Devices

#### `GET /devices`

Returns all devices with an `online` field indicating whether the agent currently has an active `Sync` stream.

```json
[
  {
    "id": "uuid",
    "hostname": "shire",
    "os": "linux",
    "mesh_ip": "100.64.0.2",
    "wg_public_key": "base64...",
    "last_endpoint": "1.2.3.4:51820",
    "last_seen": "2026-06-13T10:00:00Z",
    "agent_version": "1.0.0",
    "created_at": "2026-01-01T00:00:00Z",
    "online": true
  }
]
```

#### `GET /devices/{id}`

Returns full device details including its currently announced services and their ACL pubkeys.

```json
{
  "id": "uuid",
  "hostname": "shire",
  "os": "linux",
  "mesh_ip": "100.64.0.2",
  "wg_public_key": "base64...",
  "last_endpoint": "1.2.3.4:51820",
  "last_seen": "2026-06-13T10:00:00Z",
  "agent_version": "1.0.0",
  "online": true,
  "created_at": "2026-01-01T00:00:00Z",
  "services": [
    {
      "id": "uuid",
      "device_id": "uuid",
      "name": "files",
      "target_addr": "127.0.0.1:9010",
      "allowed_pubkeys": ["base64pubkey..."]
    }
  ]
}
```

#### `DELETE /devices/{id}`

Removes the device from the mesh and triggers a `NotifyAll` to push the updated NetworkMap to all connected agents. Returns `204 No Content`.

---

### Services

#### `GET /services`

Returns all services across all devices, each including the host device's hostname and mesh IP.

```json
[
  {
    "id": "uuid",
    "device_id": "uuid",
    "name": "files",
    "target_addr": "127.0.0.1:9010",
    "allowed_pubkeys": ["base64pubkey..."],
    "hostname": "shire",
    "mesh_ip": "100.64.0.2",
    "online": true
  }
]
```

---

### Preauth keys

#### `GET /preauth-keys`

```json
[
  {
    "key": "hex...",
    "label": "my-laptop",
    "reusable": false,
    "expiry": "2026-06-14T12:00:00Z",
    "used_at": null,
    "created_at": "2026-06-13T12:00:00Z"
  }
]
```

#### `POST /preauth-keys`

```json
{
  "label": "my-laptop",
  "ttl_hours": 24,
  "reusable": false
}
```

Returns `201 Created` with the key object. `ttl_hours` defaults to 24 if omitted or 0.

#### `DELETE /preauth-keys/{key}`

Returns `204 No Content`.

---

### Audit log

#### `GET /audit-log?limit=50&offset=0`

```json
[
  {
    "id": 1,
    "event": "device.enrolled",
    "device_id": "uuid",
    "metadata": {"hostname": "shire"},
    "created_at": "2026-06-13T10:00:00Z"
  }
]
```

`limit` defaults to 50 if omitted.

---

## gRPC API

The gRPC server on `:50051` is used by agents only (enrollment + sync). It runs in plaintext — put TLS termination in front of it (nginx, Caddy) if exposing over the internet.

Inspect with `grpcurl`:

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 zeta.v1.CoordinatorService/GetServerKey
```

### `GetServerKey`

Returns the controller CA's public key. Used by agents to verify enrollment responses.

### `Register(RegisterRequest) → RegisterResponse`

Enrolls a device. Validates the preauth key, allocates a mesh IP, signs a device certificate, and returns `NodeConfig`. See [architecture.md](architecture.md#enrollment-flow) for the full sequence.

### `Sync(stream SyncUpdate) → stream SyncResponse`

Bidirectional stream. Agents send `EndpointUpdate`, `ServiceAnnounce`, and `PingUpdate` messages. The controller sends `NetworkMap` updates. The stream is the heartbeat — `last_seen` is updated on every received message.

---

## Admin UI

The SvelteKit admin UI is embedded in the `zeta-controller` binary and served at `/`. It provides:

- **Dashboard** — device count, online status
- **Devices** — list, view details, delete
- **Services** — view all announced services across all devices (read-only)
- **Preauth Keys** — create, list, delete
- **Audit Log** — paginated event history
