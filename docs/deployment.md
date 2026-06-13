# Deployment

## Requirements

### Controller

- Linux (any distro), macOS, or Windows
- 512 MB RAM minimum; SQLite is lightweight
- Ports `8080` (REST/UI) and `50051` (gRPC) reachable by agents
- Persistent storage for the SQLite database

### Agent

- Linux only
- Root or `CAP_NET_ADMIN` + `CAP_SYS_MODULE` + `/dev/net/tun`
- UDP port `51820` (WireGuard) reachable from other peers — or at minimum STUN-punchable
- Outbound TCP to controller `:50051`

---

## Docker Compose

The recommended deployment for most setups. See `docker-compose.yml` in the repo root.

### Controller

```yaml
services:
  controller:
    image: ghcr.io/kreativethinker/zeta/controller:latest
    ports:
      - "8080:8080"
      - "50051:50051"
    volumes:
      - zeta-data:/data
    environment:
      ZETA_DB_PATH: /data/zeta.db

volumes:
  zeta-data:
```

### Agent

The agent must use `--network host` so the WireGuard interface is on the host network stack, not inside Docker's bridge.

```yaml
services:
  agent:
    image: ghcr.io/kreativethinker/zeta/agent:latest
    network_mode: host
    privileged: true
    volumes:
      - zeta-agent-state:/var/lib/zeta
      - ./zetafile.yml:/etc/zeta/zetafile.yml
    environment:
      ZETA_COORDINATOR: <controller-ip>:50051
    command: ["--zetafile", "/etc/zeta/zetafile.yml"]

volumes:
  zeta-agent-state:
```

Add `--preauth-key <key>` to `command` on the first run. Remove it after the state volume is populated.

---

## Bare metal / systemd

### Controller

```ini
# /etc/systemd/system/zeta-controller.service
[Unit]
Description=Zeta Controller
After=network.target

[Service]
ExecStart=/usr/local/bin/zeta-controller --config /etc/zeta/controller.yaml
Restart=always
RestartSec=5
User=zeta
StateDirectory=zeta

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd -r -s /sbin/nologin zeta
sudo cp zeta-controller /usr/local/bin/
sudo systemctl enable --now zeta-controller
```

### Agent

```ini
# /etc/systemd/system/zeta-agent.service
[Unit]
Description=Zeta Agent
After=network.target

[Service]
ExecStart=/usr/local/bin/zeta-agent \
  --config /etc/zeta/agent.yaml \
  --zetafile /etc/zeta/zetafile.yml
Restart=always
RestartSec=5
# Root required for WireGuard interface management
User=root
StateDirectory=zeta

[Install]
WantedBy=multi-user.target
```

```bash
sudo cp zeta-agent /usr/local/bin/
# First run: enroll manually
sudo zeta-agent --coordinator <controller>:50051 --preauth-key <key>
# Then enable the service
sudo systemctl enable --now zeta-agent
```

---

## Caddy as mesh ingress

If Caddy runs on the same host as an agent, it can serve as the HTTP(S) entry point for mesh services. Since Caddy binds to `0.0.0.0:80/443`, it receives traffic arriving via the WireGuard mesh interface — no extra port forwarding needed.

### Caddyfile

```
*.shire.mesh {
    tls internal
    reverse_proxy host.docker.internal:1080
}
```

- `tls internal` — Caddy's built-in CA issues a self-signed cert. No public DNS required.
- `host.docker.internal` — resolves to the Docker host from inside the Caddy container.

### Caddy Docker service additions

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

### Trusting the Caddy CA

`tls internal` generates a self-signed cert signed by Caddy's local CA. Export and install the root on each client device:

```bash
# Copy cert out of container
docker cp caddy:/data/caddy/pki/authorities/local/root.crt caddy-mesh-ca.crt

# Linux (system-wide, covers Chrome/Chromium)
sudo cp caddy-mesh-ca.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates

# Firefox (maintains its own trust store)
# Settings → Privacy & Security → Certificates → View Certificates → Import
```

After importing, `https://files.shire.mesh` loads without browser warnings.

### How the proxy chain works

```
Browser (graveyard)
  │  DNS: files.shire.mesh → 100.64.0.2
  │  HTTPS to 100.64.0.2:443
  ▼
WireGuard tunnel (encrypted)
  ▼
shire host — Docker iptables DNAT → Caddy container :443
  │  TLS termination
  │  X-Forwarded-For: 100.64.0.3  (graveyard's mesh IP)
  │  reverse_proxy → host.docker.internal:1080
  ▼
Zeta agent proxy :1080
  │  Host: files.shire.mesh → service "files"
  │  ACL: graveyard's pubkey ∈ allowedPKs["files"]?
  ▼
Backend: 127.0.0.1:9010
```

---

## Exposing the controller securely

The controller's gRPC port (`:50051`) and REST/UI port (`:8080`) should not be exposed to the internet without TLS. Options:

### Caddy in front of the controller

```
zeta.example.com {
    reverse_proxy localhost:8080
}
```

For the gRPC port, use Caddy's `grpc` reverse proxy or an nginx stream block.

### Firewall rules

If the controller only needs to be reachable by known agent IPs, restrict `:50051` at the firewall:

```bash
ufw allow from <agent-ip> to any port 50051
ufw deny 50051
```

---

## Production checklist

- [ ] Controller SQLite database backed up (simple file copy while controller is running works; SQLite WAL mode is safe)
- [ ] Controller behind TLS (Caddy or nginx)
- [ ] Admin password set (via config or env) — empty password disables auth
- [ ] Preauth keys are short-lived and single-use
- [ ] Agent `state.json` volume is persistent and backed up — losing it requires re-enrollment
- [ ] WireGuard UDP port `51820` open in firewall on each agent host
- [ ] Controller gRPC port `50051` reachable from all agent hosts
- [ ] If using Caddy for mesh HTTPS: Caddy CA root imported on all client devices

---

## Building from source

**Prerequisites:** Go 1.22+, Node.js 20+, `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` (proto regeneration only)

```bash
git clone https://github.com/KreativeThinker/zeta
cd zeta

# Install frontend dependencies
make ui-install

# Build everything (controller + agent binaries in dist/)
make build
```

Individual targets:

| Target | Description |
|---|---|
| `make controller` | Build controller binary (includes UI build) |
| `make agent` | Build agent binary |
| `make controller-dev` | Run controller in dev mode (no UI rebuild) |
| `make ui-dev` | Run frontend dev server with hot reload |
| `make proto` | Regenerate Go from protobuf definitions |
| `make test` | Run all tests |
| `make clean` | Remove build artifacts |

During development, run `make controller-dev` and `make ui-dev` in separate terminals. The frontend proxies API requests to `localhost:8080`.
