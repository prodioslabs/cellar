# Cellar — build, proto, and test helpers

BIN_DIR     ?= bin
CELLARD     := $(BIN_DIR)/cellard
CELLAR      := $(BIN_DIR)/cellar
CELLAR_GATEWAY := $(BIN_DIR)/cellar-gateway

DESTDIR     ?=
SYSTEMDUNITDIR ?= $(DESTDIR)/usr/lib/systemd/system
SYSUSERSDIR    ?= $(DESTDIR)/usr/lib/sysusers.d

PROTO_DIR   := api/proto
GEN_DIR     := api/gen
PROTO_SRCS  := $(wildcard $(PROTO_DIR)/*.proto)

GO          ?= go
PROTOC      ?= protoc
UNAME_S     := $(shell uname -s)

# Linux defaults to /usr/local (typically needs sudo). macOS defaults to a
# user-writable prefix (~/.local) so `make install` works without sudo.
# Prefer SUDO_USER's home when `sudo make install` is used on Darwin.
ifeq ($(UNAME_S),Darwin)
  ifneq ($(SUDO_USER),)
    DARWIN_HOME := $(shell eval echo ~$(SUDO_USER))
  else
    DARWIN_HOME := $(HOME)
  endif
  PREFIX ?= $(DARWIN_HOME)/.local
  CELLAR_DATA_DIR ?= $(DARWIN_HOME)/.cellar
  LAUNCHD_AGENT_DIR ?= $(DARWIN_HOME)/Library/LaunchAgents
  CELLAR_LOG_DIR ?= $(DARWIN_HOME)/Library/Logs/cellar
else
  PREFIX ?= /usr/local
endif

BINDIR := $(DESTDIR)$(PREFIX)/bin
# Runtime paths for launchd plist substitution (no DESTDIR).
RUNTIME_BINDIR := $(PREFIX)/bin

# Build identity — override with VERSION=… COMMIT=… BUILD_DATE=…
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/prodioslabs/cellar/internal/version
LDFLAGS    := -X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT) \
	-X $(VERSION_PKG).Date=$(BUILD_DATE)
GO_BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all build cellard cellar cellar-gateway install uninstall proto tools test clean help

all: build

help:
	@echo "Targets:"
	@echo "  make build          Build binaries into $(BIN_DIR)/"
	@echo "  make cellard        Build cellard only (CGO_ENABLED=1 for msb FFI)"
	@echo "  make cellar         Build cellar only"
	@echo "  make cellar-gateway Build cellar-gateway only"
	@echo "  make install        Install binaries (Linux: +systemd/sysusers; macOS: ~/.local/bin + LaunchAgents)"
	@echo "  make uninstall      Remove installed binaries (and Linux systemd/sysusers / macOS LaunchAgents)"
	@echo "  make proto          Regenerate gRPC stubs from $(PROTO_DIR)/"
	@echo "  make tools          Install protoc-gen-go and protoc-gen-go-grpc"
	@echo "  make test           Run go test ./..."
	@echo "  make clean          Remove built binaries"
	@echo ""
	@echo "Version overrides: VERSION=… COMMIT=… BUILD_DATE=…"

build: cellard cellar cellar-gateway

# cellard links the microsandbox Go SDK, which dlopens libkrun / related FFI.
cellard:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 $(GO) build $(GO_BUILD_FLAGS) -o $(CELLARD) ./cmd/cellard

cellar:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR) ./cmd/cellar

cellar-gateway:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o $(CELLAR_GATEWAY) ./cmd/cellar-gateway

# Installs cellar + cellard + cellar-gateway.
# Linux also installs systemd units and sysusers (does not enable/start units).
# macOS defaults PREFIX to ~/.local (no sudo) and installs LaunchAgent plists
# under ~/Library/LaunchAgents (does not load them).
install: build
ifeq ($(UNAME_S),Linux)
	install -d $(BINDIR)
	install -m 755 $(CELLAR) $(BINDIR)/cellar
	install -m 755 $(CELLARD) $(BINDIR)/cellard
	install -m 755 $(CELLAR_GATEWAY) $(BINDIR)/cellar-gateway
	install -d $(SYSTEMDUNITDIR)
	install -m 644 contrib/systemd/cellard.service $(SYSTEMDUNITDIR)/cellard.service
	install -m 644 contrib/systemd/cellar-gateway.service $(SYSTEMDUNITDIR)/cellar-gateway.service
	install -d $(SYSUSERSDIR)
	install -m 644 contrib/systemd/cellar.sysusers $(SYSUSERSDIR)/cellar.conf
else ifeq ($(UNAME_S),Darwin)
	install -d $(BINDIR)
	install -m 755 $(CELLAR) $(BINDIR)/cellar
	install -m 755 $(CELLARD) $(BINDIR)/cellard
	install -m 755 $(CELLAR_GATEWAY) $(BINDIR)/cellar-gateway
	install -d $(CELLAR_DATA_DIR)
	install -d $(LAUNCHD_AGENT_DIR) $(CELLAR_LOG_DIR)
	sed -e 's|@BINDIR@|$(RUNTIME_BINDIR)|g' \
		-e 's|@DATA_DIR@|$(CELLAR_DATA_DIR)|g' \
		-e 's|@LOG_DIR@|$(CELLAR_LOG_DIR)|g' \
		-e 's|@HOME@|$(DARWIN_HOME)|g' \
		contrib/launchd/com.prodioslabs.cellard.plist \
		> $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellard.plist
	chmod 644 $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellard.plist
	sed -e 's|@BINDIR@|$(RUNTIME_BINDIR)|g' \
		-e 's|@DATA_DIR@|$(CELLAR_DATA_DIR)|g' \
		-e 's|@LOG_DIR@|$(CELLAR_LOG_DIR)|g' \
		-e 's|@HOME@|$(DARWIN_HOME)|g' \
		contrib/launchd/com.prodioslabs.cellar-gateway.plist \
		> $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellar-gateway.plist
	chmod 644 $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellar-gateway.plist
	@echo "Installed LaunchAgents (not loaded) under $(LAUNCHD_AGENT_DIR)"
	@echo "Load with:"
	@echo "  launchctl bootstrap gui/\$$(id -u) $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellard.plist"
	@echo "  launchctl bootstrap gui/\$$(id -u) $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellar-gateway.plist   # optional"
	@case ":$$PATH:" in *"$(RUNTIME_BINDIR)"*) ;; *) \
		echo "Warning: $(RUNTIME_BINDIR) is not on PATH; add it so cellar/cellard are found." ;; \
	esac
else
	$(error make install is only supported on Linux and macOS (got $(UNAME_S)))
endif

uninstall:
ifeq ($(UNAME_S),Linux)
	rm -f $(BINDIR)/cellar $(BINDIR)/cellard $(BINDIR)/cellar-gateway
	rm -f $(SYSTEMDUNITDIR)/cellard.service $(SYSTEMDUNITDIR)/cellar-gateway.service
	rm -f $(SYSUSERSDIR)/cellar.conf
else ifeq ($(UNAME_S),Darwin)
	-launchctl bootout gui/$$(id -u)/com.prodioslabs.cellard 2>/dev/null || true
	-launchctl bootout gui/$$(id -u)/com.prodioslabs.cellar-gateway 2>/dev/null || true
	rm -f $(BINDIR)/cellar $(BINDIR)/cellard $(BINDIR)/cellar-gateway
	rm -f $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellard.plist $(LAUNCHD_AGENT_DIR)/com.prodioslabs.cellar-gateway.plist
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
	rm -rf $(BIN_DIR)
