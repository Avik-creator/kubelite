# ────────────────────────────────────────────────────────────────────────────────
# kube-lite — Makefile
# ────────────────────────────────────────────────────────────────────────────────

MODULE  := github.com/Avik-creator
BIN_DIR := bin

AGENT     := $(BIN_DIR)/agent
SCHEDULER := $(BIN_DIR)/scheduler
KL        := $(BIN_DIR)/kl

# Pass VERSION=x.y.z on the command line to embed a version string.
VERSION   ?= dev
LDFLAGS   := -ldflags "-X main.version=$(VERSION)"

# All Go source files (used to decide when to rebuild).
GO_SRCS := $(shell find . -name '*.go' -not -path './vendor/*')

.DEFAULT_GOAL := help

# ── help ────────────────────────────────────────────────────────────────────────
.PHONY: help
help:
	@echo ""
	@echo "  kube-lite make targets"
	@echo ""
	@echo "  Build"
	@echo "    make build            build all three binaries into $(BIN_DIR)/"
	@echo "    make agent            build the agent binary only"
	@echo "    make scheduler        build the scheduler binary only"
	@echo "    make kl               build the CLI binary only"
	@echo ""
  @echo "  Run (requires Docker running locally)"
	@echo "    make run-scheduler    start the scheduler on :8080"
	@echo "    make run-agent        start a single agent on :8081"
	@echo "    make dev              start scheduler + agent in tmux panes"
	@echo ""
	@echo "  UI"
	@echo "    make ui-install       npm install inside ui/"
	@echo "    make ui-dev           start Vite dev server on :5173"
	@echo "    make ui-build         production build → ui/dist/"
	@echo ""
	@echo "  Test & quality"
	@echo "    make test             run all tests"
	@echo "    make vet              go vet all packages"
	@echo "    make tidy             go mod tidy"
	@echo "    make lint             run golangci-lint (install separately)"
	@echo ""
	@echo "  Ops"
	@echo "    make clean            remove $(BIN_DIR)/"
	@echo "    make install          install binaries to \$$GOPATH/bin"
	@echo ""

# ── build ────────────────────────────────────────────────────────────────────────
.PHONY: build
build: agent scheduler kl

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

.PHONY: agent
agent: $(BIN_DIR) $(GO_SRCS)
	go build $(LDFLAGS) -o $(AGENT) ./cmd/agent

.PHONY: scheduler
scheduler: $(BIN_DIR) $(GO_SRCS)
	go build $(LDFLAGS) -o $(SCHEDULER) ./cmd/scheduler

.PHONY: kl
kl: $(BIN_DIR) $(GO_SRCS)
	go build $(LDFLAGS) -o $(KL) ./cmd/kl

# ── run ──────────────────────────────────────────────────────────────────────────
# Environment variables are read by each binary at start-up; override as needed:
#   KL_LISTEN      bind address     (agent default :8081, scheduler default :8080)
#   KL_SCHEDULER   agent → scheduler address (default localhost:8080)
#   KL_NODE_ID     agent node name  (default: hostname)

.PHONY: run-scheduler
run-scheduler: scheduler
	$(SCHEDULER)

.PHONY: run-agent
run-agent: agent
	$(AGENT)

# Convenience target: open two tmux panes side-by-side (requires tmux).
.PHONY: dev
dev: build
	@command -v tmux >/dev/null 2>&1 || { echo "tmux not found; run run-scheduler and run-agent in separate terminals"; exit 1; }
	tmux new-session -d -s kube-lite -x 220 -y 50 \; \
	  send-keys "$(MAKE) run-scheduler" Enter \; \
	  split-window -h \; \
	  send-keys "sleep 1 && $(MAKE) run-agent" Enter \; \
	  attach-session -t kube-lite

# ── ui ───────────────────────────────────────────────────────────────────────────
UI_DIR := ../ui

.PHONY: ui-install
ui-install:
	npm --prefix $(UI_DIR) install

.PHONY: ui-dev
ui-dev:
	npm --prefix $(UI_DIR) run dev

.PHONY: ui-build
ui-build:
	npm --prefix $(UI_DIR) run build

# ── test & quality ───────────────────────────────────────────────────────────────
.PHONY: test
test:
	go test ./... -count=1

.PHONY: test-verbose
test-verbose:
	go test -v ./... -count=1

.PHONY: test-race
test-race:
	go test -race ./... -count=1

.PHONY: vet
vet:
	go vet ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint not found. Install: https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run ./...

# Run vet + tidy check together (useful in CI).
.PHONY: check
check: vet tidy
	git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum changed after tidy — commit them" && exit 1)

# ── install ──────────────────────────────────────────────────────────────────────
.PHONY: install
install:
	go install $(LDFLAGS) ./cmd/agent
	go install $(LDFLAGS) ./cmd/scheduler
	go install $(LDFLAGS) ./cmd/kl

# ── clean ────────────────────────────────────────────────────────────────────────
.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
