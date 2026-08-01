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
PROTO_SRCS  := $(wildcard $(PROTO_DIR)/*.proto)

GO          ?= go
PROTOC      ?= protoc
BUN         ?= bun
UNAME_S     := $(shell uname -s)
DOCKER      ?= docker

# On macOS, stage cellar-agent under the user data dir so Docker Desktop can
# bind-mount it. Prefer SUDO_USER's home when `sudo make install` is used.
ifeq ($(UNAME_S),Darwin)
  ifneq ($(SUDO_USER),)
    DARWIN_HOME := $(shell eval echo ~$(SUDO_USER))
  else
    DARWIN_HOME := $(HOME)
  endif
  CELLAR_DATA_DIR ?= $(DARWIN_HOME)/.cellar
endif

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

# Egress image tarball (release CI / local repro). Override EGRESS_IMAGE_ARCH,
# EGRESS_IMAGE_TAG, or EGRESS_TARBALL as needed.
EGRESS_IMAGE_ARCH ?= $(shell $(GO) env GOARCH 2>/dev/null || echo amd64)
EGRESS_PLATFORM ?= linux/$(EGRESS_IMAGE_ARCH)
EGRESS_IMAGE_TAG ?= $(VERSION)
EGRESS_FILE_VERSION ?= $(patsubst v%,%,$(VERSION))
EGRESS_TARBALL ?= cellar-egress-gateway-image_$(EGRESS_FILE_VERSION)_linux_$(EGRESS_IMAGE_ARCH).tar.gz

.PHONY: all build build-sdk cellard cellar cellar-agent cellar-gateway cellar-egress-gateway egress-gateway-image egress-gateway-image-tarball install uninstall proto tools test clean help sdk-node

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
	@echo "  make egress-gateway-image-tarball  Build + docker save $(EGRESS_IMAGE) as .tar.gz"
	@echo "  make sdk-node       Build the Node SDK"
	@echo "  make install        Install binaries (Linux: +systemd/sysusers; macOS: stage agent under ~/.cellar)"
	@echo "  make uninstall      Remove installed binaries (and Linux systemd/sysusers drop-ins)"
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
	CGO_ENABLED=0 GOOS=linux $(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_AGENT) ./cmd/cellar-agent

cellar-gateway:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_GATEWAY) ./cmd/cellar-gateway

cellar-egress-gateway:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_EGRESS_GATEWAY) ./cmd/cellar-egress-gateway

egress-gateway-image:
	$(DOCKER) build -f images/egress-gateway/Dockerfile -t $(EGRESS_IMAGE):$(VERSION) -t $(EGRESS_IMAGE):latest .

# Build a single-platform image and write a gzipped `docker save` archive
# (used by the release workflow; install.sh loads these with `docker load`).
egress-gateway-image-tarball:
	@mkdir -p $(dir $(EGRESS_TARBALL))
	$(DOCKER) buildx build --platform $(EGRESS_PLATFORM) \
		-f images/egress-gateway/Dockerfile \
		-t $(EGRESS_IMAGE):$(EGRESS_IMAGE_TAG) \
		-t $(EGRESS_IMAGE):latest \
		--load .
	$(DOCKER) save $(EGRESS_IMAGE):latest $(EGRESS_IMAGE):$(EGRESS_IMAGE_TAG) | gzip > $(EGRESS_TARBALL)
	@echo "Wrote $(EGRESS_TARBALL)"

sdk-node:
	cd $(SDK_NODE_DIR) && $(BUN) run build

# Installs cellar + cellard + cellar-gateway + cellar-agent next to cellard.
# Linux also installs cellar-egress-gateway, systemd units, and sysusers (does
# not enable/start units). macOS additionally stages cellar-agent under
# $(CELLAR_DATA_DIR) so Docker Desktop can bind-mount it.
install: build
ifeq ($(UNAME_S),Linux)
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
else ifeq ($(UNAME_S),Darwin)
	install -d $(BINDIR)
	install -m 755 $(CELLAR) $(BINDIR)/cellar
	install -m 755 $(CELLARD) $(BINDIR)/cellard
	install -m 755 $(CELLAR_AGENT) $(BINDIR)/cellar-agent
	install -m 755 $(CELLAR_GATEWAY) $(BINDIR)/cellar-gateway
	install -d $(CELLAR_DATA_DIR)
	install -m 755 $(CELLAR_AGENT) $(CELLAR_DATA_DIR)/cellar-agent
	@echo "Staged cellar-agent for Docker Desktop at $(CELLAR_DATA_DIR)/cellar-agent"
else
	$(error make install is only supported on Linux and macOS (got $(UNAME_S)))
endif

uninstall:
ifeq ($(UNAME_S),Linux)
	rm -f $(BINDIR)/cellar $(BINDIR)/cellard $(BINDIR)/cellar-agent $(BINDIR)/cellar-gateway $(BINDIR)/cellar-egress-gateway
	rm -f $(SYSTEMDUNITDIR)/cellard.service $(SYSTEMDUNITDIR)/cellar-gateway.service
	rm -f $(SYSUSERSDIR)/cellar.conf
else ifeq ($(UNAME_S),Darwin)
	rm -f $(BINDIR)/cellar $(BINDIR)/cellard $(BINDIR)/cellar-agent $(BINDIR)/cellar-gateway
	rm -f $(CELLAR_DATA_DIR)/cellar-agent
else
	$(error make uninstall is only supported on Linux and macOS (got $(UNAME_S)))
endif

tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

proto: $(PROTO_SRCS)
	@mkdir -p $(GEN_DIR)
	$(PROTOC) -I $(PROTO_DIR) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		$(PROTO_SRCS)

test:
	$(GO) test ./...

clean:
	rm -rf $(BIN_DIR) $(SDK_NODE_DIR)/dist
