# Development Guide

Environment setup, build instructions, and workflow for all components of Zeta.

---

## Prerequisites

### Core tools (all components)

| Tool | Minimum version | Install |
|---|---|---|
| Go | 1.22 | [go.dev/dl](https://go.dev/dl) |
| Node.js | 20 LTS | [nodejs.org](https://nodejs.org) or `nvm` |
| npm | 9+ | bundled with Node.js |
| Git | any recent | system package manager |

### Linux agent (additional)

The agent creates WireGuard interfaces and manages kernel routes. On your dev machine you can build and run tests without root, but actually running the agent requires:

- Linux with WireGuard kernel module (`modprobe wireguard` or kernel ≥ 5.6)
- Root or `CAP_NET_ADMIN` + `CAP_SYS_MODULE` + `/dev/net/tun`
- `systemd-resolved` for DNS split-zone integration (standard on Ubuntu 20.04+, Fedora, Arch)

### Protobuf (only if editing `.proto` files)

```bash
# Install protoc
# Ubuntu / Debian
sudo apt install -y protobuf-compiler

# macOS
brew install protobuf

# Install Go plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`.

---

## Repository layout

```
zeta/
├── agent/          Linux agent (Go)
├── android/        Android client (Kotlin + Go)
│   ├── app/        Android app module
│   └── vpnlib/     Go library built as an AAR via gomobile
├── controller/     Coordination server (Go)
├── frontend/       Admin UI (SvelteKit)
├── proto/          Protobuf definitions
├── docs/           Documentation
├── Makefile
└── docker-compose.yml
```

---

## Getting started

```bash
git clone https://github.com/KreativeThinker/zeta
cd zeta

# Install frontend dependencies (needed for controller build)
make ui-install
```

---

## Controller

### Build

```bash
make controller          # builds dist/zeta-controller (includes embedded UI)
make controller-dev      # run without rebuilding UI (fast iteration)
make ui-dev              # run SvelteKit dev server with hot reload (separate terminal)
```

During active frontend development, run `make controller-dev` and `make ui-dev` in separate terminals. The Vite dev server proxies API calls to `localhost:8080`.

### Test

```bash
make controller-test
```

### Minimal config for local dev

No config file needed — the controller starts with sane defaults. SQLite is created at `./zeta.db` and the server listens on `:8080` (REST/UI) and `:50051` (gRPC).

```bash
cd controller && go run ./cmd/server
```

Open `http://localhost:8080` to access the admin UI.

---

## Linux agent

### Build

```bash
make agent               # builds dist/zeta-agent
```

### Test

```bash
make agent-test
```

### Running locally

The agent requires root. For development, run it against a local controller:

```bash
# First run: enroll
sudo ./dist/zeta-agent \
  --coordinator localhost:50051 \
  --preauth-key <key-from-admin-ui>

# Subsequent runs
sudo ./dist/zeta-agent --coordinator localhost:50051
```

### Verifying the agent

```bash
sudo wg show zeta0           # WireGuard interface + peers
ip addr show zeta0           # assigned mesh IP
ip route | grep 100.64       # mesh route
dig @127.0.0.1 shire.mesh    # DNS resolution
```

---

## Android client

### Prerequisites

In addition to Go, you need:

| Tool | Minimum version | Notes |
|---|---|---|
| Android Studio | Ladybug (2024.2) or later | Or just the SDK command-line tools |
| Android SDK | API 35 | Install via Android Studio or `sdkmanager` |
| Android NDK | 27.x | Required for gomobile — install via `sdkmanager "ndk;27.x.x"` |
| JDK | 17 | Bundled with Android Studio; or `sudo apt install openjdk-17-jdk` |
| gomobile | latest | `go install golang.org/x/mobile/cmd/gomobile@latest` |
| gobind | latest | `go install golang.org/x/mobile/cmd/gobind@latest` |

Set `ANDROID_HOME` to your SDK path (Android Studio sets this automatically):

```bash
export ANDROID_HOME=$HOME/android-sdk     # or $HOME/Library/Android/sdk on macOS
```

### Project structure

```
android/
├── app/
│   ├── libs/vpnlib.aar     # pre-built Go AAR (committed); rebuild if vpnlib/ changes
│   └── src/main/kotlin/com/zeta/android/
│       ├── ZetaApplication.kt
│       ├── MainActivity.kt
│       ├── data/           state management, WireGuard key generation
│       ├── net/            OkHttp DNS resolver for in-app mesh requests
│       ├── repository/     gRPC sync + NetworkMap
│       ├── ui/             Compose screens (Dashboard, Peers, Services, WebView)
│       └── vpn/            ZetaVpnService — TUN setup + vpnlib calls
└── vpnlib/                 Go module — compiled to vpnlib.aar via gomobile bind
    ├── android_tun.go      Custom tun.Device (bypasses TUNGETIFF ioctl)
    ├── filtered_tun.go     Intercepts DNS packets from the TUN read path
    ├── dns.go              In-memory *.mesh resolver + upstream forwarder
    └── vpnlib.go           gomobile-exported API: Start/Stop/SetConfig/SetDNSRecords
```

### Building the AAR (vpnlib)

The AAR is committed to the repo at `android/app/libs/vpnlib.aar`. Only rebuild it if you change files under `android/vpnlib/`.

```bash
cd android/vpnlib

# Initialize gomobile (first time only)
GOWORK=off gomobile init

# Build AAR for all Android ABIs
GOWORK=off gomobile bind \
  -target=android/arm64,android/arm,android/amd64 \
  -androidapi 26 \
  -o ../app/libs/vpnlib.aar \
  .
```

`GOWORK=off` is required because this module is not part of the root `go.work` workspace.

### Building the Android app

```bash
cd android
./gradlew assembleDebug      # debug APK at app/build/outputs/apk/debug/
./gradlew assembleRelease    # release APK (requires signing config)
./gradlew installDebug       # build and install on connected device/emulator
```

### Signing a release build

Set these environment variables before building:

```bash
export KEYSTORE_FILE=/path/to/release.jks
export KEYSTORE_PASSWORD=...
export KEY_ALIAS=...
export KEY_PASSWORD=...

./gradlew assembleRelease
```

### Architecture note: split DNS

The Android client owns the VPN TUN fd directly (via `VpnService.Builder.establish().detachFd()`). The Go library intercepts DNS packets destined for the virtual IP `100.64.0.1:53` at the TUN packet level — this is the only way to serve DNS on Android without root, since non-root apps cannot bind port 53. Resolved `.mesh` names come from the NetworkMap; all other queries are forwarded to the system's upstream DNS captured before the VPN started.

See [architecture.md](architecture.md#android-client) for the full data flow.

---

## Protobuf

Only needed when changing `proto/zeta.proto`.

```bash
make proto
```

This regenerates `proto/zetapb/*.go`. Commit both the `.proto` file and the generated Go code.

---

## Full build

```bash
make build        # controller + agent binaries in dist/
```

---

## Development workflow

### Typical backend change

1. Edit agent or controller code.
2. Run `make controller-test` or `make agent-test`.
3. Start controller: `make controller-dev`
4. Start a local agent: `sudo ./dist/zeta-agent --coordinator localhost:50051`
5. Test via admin UI at `http://localhost:8080`.

### Typical frontend change

1. Start controller: `make controller-dev`
2. Start Vite dev server: `make ui-dev`
3. Open `http://localhost:5173` — changes hot-reload.
4. When done: `make ui-build` embeds the frontend into the controller binary.

### Typical Android change

1. Edit Kotlin source in `android/app/`.
2. If you changed `android/vpnlib/`, rebuild the AAR (see above) and commit the updated `vpnlib.aar`.
3. Run `./gradlew installDebug` to push to a connected device.
4. Check logcat: `adb logcat -s ZetaVpnService ZetaStun MeshWebView`

### Typical proto change

1. Edit `proto/zeta.proto`.
2. `make proto` — regenerates `proto/zetapb/`.
3. Fix any compilation errors in `agent/` and `controller/` that result from the schema change.
4. Commit `zeta.proto` and `proto/zetapb/*.go` together.

---

## Linting

```bash
make lint           # all components

# Individual
make controller-lint
make agent-lint
make ui-check
make proto-lint     # requires buf: https://buf.build/docs/installation
```

Go linting uses `golangci-lint`:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

---

## Docker development

Build images locally:

```bash
docker build -f controller/Dockerfile -t zeta-controller:dev .
docker build -f agent/Dockerfile -t zeta-agent:dev .
```

Run with local images:

```bash
CONTROLLER_IMAGE=zeta-controller:dev docker compose up controller
```

---

## Troubleshooting

**`make controller` fails with missing `npm`**
Install Node.js ≥ 20. The controller build embeds the SvelteKit frontend.

**Agent fails with `operation not permitted` on `ip link add`**
You need root or `CAP_NET_ADMIN`. Run with `sudo`.

**`systemd-resolved` doesn't pick up `.mesh` DNS**
The agent writes `/etc/systemd/resolved.conf.d/zeta.conf` on startup. Check it exists and run `sudo systemctl restart systemd-resolved`.

**gomobile bind: `gobind was not found`**
Run `go install golang.org/x/mobile/cmd/gobind@latest`, ensure `$(go env GOPATH)/bin` is on your `PATH`.

**Android build: `Unresolved reference 'Vpnlib'`**
The AAR at `android/app/libs/vpnlib.aar` is missing or stale. Rebuild it (see [Building the AAR](#building-the-aar-vpnlib)).

**WireGuard peers not connecting**
Check that UDP port 51820 is open on both hosts. Run `sudo wg show zeta0` and confirm the peer has a recent handshake time. If the handshake never occurs, the STUN-discovered endpoint may be blocked — check NAT/firewall rules.
