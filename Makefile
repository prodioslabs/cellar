# Cellar — build, proto, and test helpers

BIN_DIR     ?= bin
CELLARD     := $(BIN_DIR)/cellard
CELLAR      := $(BIN_DIR)/cellar
CELLAR_AGENT := $(BIN_DIR)/cellar-agent
CELLAR_GATEWAY := $(BIN_DIR)/cellar-gateway
CELLAR_EGRESS_GATEWAY := $(BIN_DIR)/cellar-egress-gateway

PREFIX      ?= /usr/local
DESTDIR     ?=
BINDIR      := $(DESTDIR)$(PREFIX)/bin
SYSTEMDUNITDIR ?= $(DESTDIR)/usr/lib/systemd/system
SYSUSERSDIR    ?= $(DESTDIR)/usr/lib/sysusers.d

PROTO_DIR   := api/proto
GEN_DIR     := api/gen
AGENT_PROTO := $(PROTO_DIR)/agent.proto
PROTO_SRCS  := $(filter-out $(AGENT_PROTO),$(wildcard $(PROTO_DIR)/*.proto))

GO          ?= go
PROTOC      ?= protoc
BUN         ?= bun
UNAME_S     := $(shell uname -s)
DOCKER      ?= docker

SDK_NODE_DIR := sdk/node
EGRESS_IMAGE ?= cellar/egress-gateway

# Build identity — override with VERSION=… COMMIT=… BUILD_DATE=…
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/prodioslabs/cellar/internal/version
LDFLAGS    := -X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT) \
	-X $(VERSION_PKG).Date=$(BUILD_DATE)
GO_BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all build build-sdk cellard cellar cellar-agent cellar-gateway cellar-egress-gateway egress-gateway-image install uninstall proto tools test clean help sdk-node

all: build

help:
	@echo "Targets:"
	@echo "  make build          Build binaries into $(BIN_DIR)/"
	@echo "  make build-sdk      Build all SDKs"
	@echo "  make cellard        Build cellard only"
	@echo "  make cellar         Build cellar only"
	@echo "  make cellar-agent   Build cellar-agent (static, for sandbox injection)"
	@echo "  make cellar-gateway Build cellar-gateway only"
	@echo "  make cellar-egress-gateway Build cellar-egress-gateway binary"
	@echo "  make egress-gateway-image  Build $(EGRESS_IMAGE) Docker image"
	@echo "  make sdk-node       Build the Node SDK"
	@echo "  make install        Install binaries, systemd units, and sysusers drop-in (Linux)"
	@echo "  make uninstall      Remove installed binaries, systemd units, and sysusers drop-in (Linux)"
	@echo "  make proto          Regenerate gRPC stubs from $(PROTO_DIR)/"
	@echo "  make tools          Install protoc-gen-go and protoc-gen-go-grpc"
	@echo "  make test           Run go test ./..."
	@echo "  make clean          Remove built binaries and Node SDK output"
	@echo ""
	@echo "Version overrides: VERSION=… COMMIT=… BUILD_DATE=…"

build: cellard cellar cellar-agent cellar-gateway cellar-egress-gateway

build-sdk: sdk-node

cellard:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(CELLARD) ./cmd/cellard

cellar:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR) ./cmd/cellar

cellar-agent:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_AGENT) ./cmd/cellar-agent

cellar-gateway:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_GATEWAY) ./cmd/cellar-gateway

cellar-egress-gateway:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_EGRESS_GATEWAY) ./cmd/cellar-egress-gateway

egress-gateway-image:
	$(DOCKER) build -f images/egress-gateway/Dockerfile -t $(EGRESS_IMAGE):$(VERSION) -t $(EGRESS_IMAGE):latest .

sdk-node:
	cd $(SDK_NODE_DIR) && $(BUN) run build

# Linux only for now. Installs cellar + cellard + cellar-gateway, plus cellar-agent
# (required next to cellard; default lookup is $(PREFIX)/bin/cellar-agent), and the
# systemd units + sysusers drop-in. Does not run systemctl enable/start.
install: build
ifneq ($(UNAME_S),Linux)
	$(error make install is currently only supported on Linux (got $(UNAME_S)))
endif
	install -d $(BINDIR)
	install -m 755 $(CELLAR) $(BINDIR)/cellar
	install -m 755 $(CELLARD) $(BINDIR)/cellard
	install -m 755 $(CELLAR_AGENT) $(BINDIR)/cellar-agent
	install -m 755 $(CELLAR_GATEWAY) $(BINDIR)/cellar-gateway
	install -m 755 $(CELLAR_EGRESS_GATEWAY) $(BINDIR)/cellar-egress-gateway
	install -d $(SYSTEMDUNITDIR)
	install -m 644 contrib/systemd/cellard.service $(SYSTEMDUNITDIR)/cellard.service
	install -m 644 contrib/systemd/cellar-gateway.service $(SYSTEMDUNITDIR)/cellar-gateway.service
	install -d $(SYSUSERSDIR)
	install -m 644 contrib/systemd/cellar.sysusers $(SYSUSERSDIR)/cellar.conf

uninstall:
ifneq ($(UNAME_S),Linux)
	$(error make uninstall is currently only supported on Linux (got $(UNAME_S)))
endif
	rm -f $(BINDIR)/cellar $(BINDIR)/cellard $(BINDIR)/cellar-agent $(BINDIR)/cellar-gateway $(BINDIR)/cellar-egress-gateway
	rm -f $(SYSTEMDUNITDIR)/cellard.service $(SYSTEMDUNITDIR)/cellar-gateway.service
	rm -f $(SYSUSERSDIR)/cellar.conf

tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

proto: $(PROTO_SRCS) $(AGENT_PROTO)
	@mkdir -p $(GEN_DIR) $(GEN_DIR)/agent
	$(PROTOC) -I $(PROTO_DIR) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		$(PROTO_SRCS)
	$(PROTOC) -I $(PROTO_DIR) \
		--go_out=$(GEN_DIR)/agent --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR)/agent --go-grpc_opt=paths=source_relative \
		$(AGENT_PROTO)

test:
	$(GO) test ./...

clean:
	rm -rf $(BIN_DIR) $(SDK_NODE_DIR)/dist
