# Stage 1: Build the Go agent binary
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy workspace files
COPY go.work go.work.sum ./
COPY proto/go.mod proto/go.sum proto/
COPY agent/go.mod agent/go.sum agent/
COPY controller/go.mod controller/go.sum controller/

# Download deps (layer-cached until go.mod changes)
RUN go work sync

COPY proto/ proto/
COPY agent/ agent/
COPY controller/ controller/

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/zeta-agent \
    ./agent/cmd/agent

# Stage 2: Minimal runtime image
# Uses alpine (not scratch) — we need kernel module tools and ip/wg utilities.
FROM alpine:3.21
RUN apk add --no-cache wireguard-tools iproute2

COPY --from=builder /out/zeta-agent /usr/local/bin/zeta-agent

VOLUME ["/var/lib/zeta"]

ENV ZETA_STATE_PATH=/var/lib/zeta/state.json

# Agent needs NET_ADMIN + SYS_MODULE + /dev/net/tun (or --privileged).
# Pass --preauth-key on first run; subsequent runs load state from volume.
ENTRYPOINT ["zeta-agent"]
