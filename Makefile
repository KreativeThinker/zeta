SHELL := bash
.DEFAULT_GOAL := help

CONTROLLER_BIN := dist/zeta-controller
AGENT_BIN      := dist/zeta-agent
VERSION        ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT         ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS        := -trimpath -ldflags="-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)"

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Proto ──────────────────────────────────────────────────────────────────────

.PHONY: proto
proto: ## Generate Go code from proto definitions
	mkdir -p proto/zetapb
	protoc \
		--go_out=proto/zetapb \
		--go_opt=paths=source_relative \
		--go-grpc_out=proto/zetapb \
		--go-grpc_opt=paths=source_relative \
		-I proto proto/zeta.proto

.PHONY: proto-lint
proto-lint: ## Lint proto files (requires buf)
	cd proto && buf lint

# ── Frontend ───────────────────────────────────────────────────────────────────

.PHONY: ui-install
ui-install: ## Install frontend dependencies
	cd frontend && npm ci

.PHONY: ui-dev
ui-dev: ## Start frontend dev server
	cd frontend && npm run dev

.PHONY: ui-build
ui-build: ## Build frontend static files into controller/web/dist
	cd frontend && npm run build

.PHONY: ui-check
ui-check: ## Type-check and lint frontend
	cd frontend && npm run check

# ── Controller ─────────────────────────────────────────────────────────────────

.PHONY: controller
controller: ui-build ## Build controller binary (includes embedded UI)
	mkdir -p dist
	cd controller && CGO_ENABLED=0 go build $(LDFLAGS) -o ../$(CONTROLLER_BIN) ./cmd/server

.PHONY: controller-dev
controller-dev: ## Run controller without rebuilding UI
	cd controller && go run ./cmd/server

.PHONY: controller-test
controller-test: ## Run controller tests
	cd controller && go test ./... -race

.PHONY: controller-lint
controller-lint: ## Lint controller
	cd controller && golangci-lint run ./...

# ── Agent ──────────────────────────────────────────────────────────────────────

.PHONY: agent
agent: ## Build Linux agent binary
	mkdir -p dist
	cd agent && CGO_ENABLED=0 go build $(LDFLAGS) -o ../$(AGENT_BIN) ./cmd/agent

.PHONY: agent-test
agent-test: ## Run agent tests
	cd agent && go test ./... -race

.PHONY: agent-lint
agent-lint: ## Lint agent
	cd agent && golangci-lint run ./...

# ── Combined ───────────────────────────────────────────────────────────────────

.PHONY: build
build: controller agent ## Build all binaries

.PHONY: test
test: controller-test agent-test ## Run all tests

.PHONY: lint
lint: proto-lint controller-lint agent-lint ui-check ## Lint everything

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf dist/ controller/web/dist/ frontend/.svelte-kit/ frontend/build/
