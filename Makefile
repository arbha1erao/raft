BIN_DIR := bin
SRC_DIRS := client clustercfg cmd raft utils
BINARIES := $(BIN_DIR)/raft $(BIN_DIR)/clustercfg
CONFIG_FILE := cluster_config.json

.PHONY: all build clean clean-config lint help

all: build

build: $(BIN_DIR) $(BINARIES)

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

$(BIN_DIR)/raft: cmd/raft/main.go
	@echo "Building raft binary..."
	@go build -o $@ $<

$(BIN_DIR)/clustercfg: cmd/clustercfg/main.go
	@echo "Building clustercfg binary..."
	@go build -o $@ $<

clean:
	@echo "Cleaning binaries..."
	@rm -rf $(BIN_DIR)

clean-cfg:
	@echo "Removing configuration file..."
	@rm -f $(CONFIG_FILE)

lint:
	@echo "Running linter..."
	@golangci-lint run || echo "Linter found issues!"

help:
	@echo "Available make targets:"
	@echo "  build         Build binaries in the bin folder"
	@echo "  clean         Remove all binaries"
	@echo "  clean-config  Remove configuration file"
	@echo "  lint          Run Go linter"
	@echo "  help          Show this help message"
