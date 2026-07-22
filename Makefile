# Cellar — build, proto, and test helpers

BIN_DIR     ?= bin
CELLARD     := $(BIN_DIR)/cellard
CELLAR      := $(BIN_DIR)/cellar

PROTO_DIR   := api/proto
GEN_DIR     := api/gen
PROTO_SRCS  := $(wildcard $(PROTO_DIR)/*.proto)

GO          ?= go
PROTOC      ?= protoc

.PHONY: all build cellard cellar proto tools test clean help

all: build

help:
	@echo "Targets:"
	@echo "  make build    Build cellard and cellar into $(BIN_DIR)/"
	@echo "  make cellard  Build cellard only"
	@echo "  make cellar   Build cellar only"
	@echo "  make proto    Regenerate gRPC stubs from $(PROTO_DIR)/"
	@echo "  make tools    Install protoc-gen-go and protoc-gen-go-grpc"
	@echo "  make test     Run go test ./..."
	@echo "  make clean    Remove built binaries"

build: cellard cellar

cellard:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(CELLARD) ./cmd/cellard

cellar:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(CELLAR) ./cmd/cellar

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
