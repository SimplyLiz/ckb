CARTOGRAPHER_DIR := third_party/cartographer
CARTOGRAPHER_LIB := $(CARTOGRAPHER_DIR)/target/release/libcode_cartographer.a
# Pinned CodeCartographer release whose prebuilt static library CKB links.
CARTOGRAPHER_VERSION ?= v4.0.3
BIN_DIR := bin

.PHONY: build fetch-cartographer-lib build-cartographer-source build-fast test test-cartographer lint clean check-cartographer

## Build CKB with the cartographer tier (default). Fetches the pinned prebuilt
## static library — no Rust toolchain required.
build: fetch-cartographer-lib
	@mkdir -p $(BIN_DIR)
	go build -tags cartographer -o $(BIN_DIR)/ckb ./cmd/ckb/...
	@echo "Built: $(BIN_DIR)/ckb (with cartographer)"

## Fetch the pinned, checksum-verified prebuilt cartographer static library +
## header (already tree-sitter-localized upstream). No Rust toolchain needed.
fetch-cartographer-lib:
	@CARTOGRAPHER_VERSION=$(CARTOGRAPHER_VERSION) bash scripts/fetch-cartographer-lib.sh

## Build the static library from vendored source (requires Rust) — for
## co-developing the engine. The default `build` uses the pinned prebuilt lib.
build-cartographer-source:
	@echo "Building cartographer static library from source..."
	@cd $(CARTOGRAPHER_DIR) && cargo build --release
	@echo "Localizing tree-sitter symbols (prevents link-time collisions with go-tree-sitter)..."
	@cd $(CARTOGRAPHER_DIR) && bash scripts/localize-tree-sitter-symbols.sh target/release/libcode_cartographer.a
	@echo "Library: $(CARTOGRAPHER_LIB)"

## Build without cartographer (no Rust toolchain required — for CI and contributors)
build-fast:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ckb ./cmd/ckb/...
	@echo "Built: $(BIN_DIR)/ckb (without cartographer)"

## Run all tests
test:
	go test ./...

## Run all tests with cartographer compiled in (fetches the prebuilt lib)
test-cartographer: fetch-cartographer-lib
	go test -tags cartographer ./...

## Lint
lint:
	golangci-lint run ./...

## Remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## Check whether the cartographer library is present
check-cartographer:
	@if [ -f "$(CARTOGRAPHER_LIB)" ]; then \
		echo "cartographer library found: $(CARTOGRAPHER_LIB)"; \
	else \
		echo "cartographer library NOT found. Run: make build-cartographer"; \
		exit 1; \
	fi
