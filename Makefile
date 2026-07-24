# Cellar — build, proto, and test helpers

BIN_DIR     ?= bin
CELLARD     := $(BIN_DIR)/cellard
CELLAR      := $(BIN_DIR)/cellar
CELLAR_AGENT := $(BIN_DIR)/cellar-agent

PROTO_DIR   := api/proto
GEN_DIR     := api/gen
AGENT_PROTO := $(PROTO_DIR)/agent.proto
PROTO_SRCS  := $(filter-out $(AGENT_PROTO),$(wildcard $(PROTO_DIR)/*.proto))

GO          ?= go
PROTOC      ?= protoc

.PHONY: all build cellard cellar cellar-agent proto tools test clean help

all: build

help:
	@echo "Targets:"
	@echo "  make build         Build cellard, cellar, and cellar-agent into $(BIN_DIR)/"
	@echo "  make cellard       Build cellard only"
	@echo "  make cellar        Build cellar only"
	@echo "  make cellar-agent  Build cellar-agent (static, for sandbox injection)"
	@echo "  make proto         Regenerate gRPC stubs from $(PROTO_DIR)/"
	@echo "  make tools         Install protoc-gen-go and protoc-gen-go-grpc"
	@echo "  make test          Run go test ./..."
	@echo "  make clean         Remove built binaries"

build: cellard cellar cellar-agent

cellard:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(CELLARD) ./cmd/cellard

cellar:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(CELLAR) ./cmd/cellar

cellar-agent:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(CELLAR_AGENT) ./cmd/cellar-agent

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
	rm -rf $(BIN_DIR)
