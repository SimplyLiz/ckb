BIN_DIR := bin

.PHONY: build build-fast test lint clean

## Build CKB
build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ckb ./cmd/ckb/...
	@echo "Built: $(BIN_DIR)/ckb"

## Same as build (kept for scripts that call it)
build-fast: build

## Run all tests
test:
	go test ./...

## Lint
lint:
	golangci-lint run ./...

## Remove build artifacts
clean:
	rm -rf $(BIN_DIR)
