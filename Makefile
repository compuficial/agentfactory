# AgentFactory (af)
#
#   make              # build ./af
#   make install      # install to ~/.local/bin + bash completion
#   make fresh        # wipe all state, then install
#   make reset        # wipe all state (tmux server + db + logs)

PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BINARY ?= af

# Runtime state (defaults match af without flags/env; AF_* env wins)
SOCKET   ?= $(or $(AF_SOCKET),af)
DATA_DIR ?= $(or $(AF_DATA_DIR),$(or $(XDG_DATA_HOME),$(HOME)/.local/share)/agentfactory)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS  = -X agentfactory.sh/af/internal/cli.Version=$(VERSION) \
           -X agentfactory.sh/af/internal/cli.Commit=$(COMMIT)

.PHONY: all help build install uninstall fresh reset test vet fmt tidy clean \
        agent-tools agent-tools-check agent-skills agent-skills-check

all: build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build ./af in the repo root
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/af

install: build ## Install af + bash completion to $(PREFIX) (default: ~/.local)
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	@mkdir -p $(PREFIX)/share/bash-completion/completions
	@$(BINDIR)/$(BINARY) completion bash > $(PREFIX)/share/bash-completion/completions/$(BINARY)
	@echo "installed $(BINDIR)/$(BINARY)"
	@echo "completion: new shells pick it up automatically; for THIS shell run:"
	@echo "  source $(PREFIX)/share/bash-completion/completions/$(BINARY)"

uninstall: ## Remove af and its completion from $(PREFIX)
	rm -f $(BINDIR)/$(BINARY) $(PREFIX)/share/bash-completion/completions/$(BINARY)

reset: ## Wipe all state: kill the af tmux server + delete data dir (db, logs)
	@echo "resetting socket=$(SOCKET) data_dir=$(DATA_DIR)"
	@tmux -L "$(SOCKET)" kill-server 2>/dev/null || true
	@rm -rf "$(DATA_DIR)"
	@echo "cleared tmux -L $(SOCKET) and $(DATA_DIR)"

fresh: reset install ## Reset state, then install this build
	@echo "fresh install ready — try: af doctor"

cover: ## Run tests with a coverage summary (writes cover.out)
	go test -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1

fuzz: ## Run the byte-parser fuzzers briefly (~20s)
	go test ./internal/core -run '^$$' -fuzz FuzzScanStreamEvents -fuzztime 10s
	go test ./internal/core -run '^$$' -fuzz FuzzSanitizeTerminal -fuzztime 10s

test: ## Run tests with -race
	go test ./... -race

vet: ## go vet
	go vet ./...

fmt: ## gofmt all packages
	gofmt -s -w .

tidy: ## go mod tidy
	go mod tidy

agent-tools: ## Install/wire rtk, codegraph, ast-grep (host agent tools)
	./scripts/setup-agent-tools.sh

agent-tools-check: ## Show rtk / codegraph / ast-grep status
	./scripts/setup-agent-tools.sh --check

agent-skills: ## Install mattpocock/skills + obra/superpowers (global)
	./scripts/setup-agent-skills.sh --global

agent-skills-check: ## List installed agent skills
	./scripts/setup-agent-skills.sh --check

clean: ## Remove local build artifacts
	rm -f $(BINARY) cover.out
