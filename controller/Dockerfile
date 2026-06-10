# Stage 1: Build the SvelteKit UI
# Run from /src so that svelte.config.js can output to ../controller/web/dist
FROM node:20-alpine AS ui-builder
WORKDIR /src
COPY frontend/package*.json frontend/
RUN cd frontend && npm ci
COPY frontend/ frontend/
RUN cd frontend && npm run build
# Output lands in /src/controller/web/dist (per svelte.config.js adapter path)

# Stage 2: Build the Go controller binary
FROM golang:1.25-alpine AS go-builder
WORKDIR /src

# Copy workspace files
COPY go.work go.work.sum ./
COPY proto/go.mod proto/go.sum proto/
COPY controller/go.mod controller/go.sum controller/
COPY agent/go.mod agent/go.sum agent/

# Download deps (layer-cached until go.mod changes)
RUN go work sync

COPY proto/ proto/
COPY controller/ controller/
COPY agent/ agent/

# Overlay built UI into the embed location (overwrites placeholder index.html)
COPY --from=ui-builder /src/controller/web/dist/ controller/web/dist/

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/zeta-controller \
    ./controller/cmd/server

# Stage 3: Minimal runtime image
FROM scratch
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=go-builder /out/zeta-controller /zeta-controller

EXPOSE 8080 50051

VOLUME ["/data"]

ENV ZETA_DB_PATH=/data/zeta.db

ENTRYPOINT ["/zeta-controller"]
