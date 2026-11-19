BIN_DIR := bin

.PHONY: build build-fast deploy test lint clean

## Build CKB
build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ckb ./cmd/ckb/...
	@echo "Built: $(BIN_DIR)/ckb"

## Same as build (kept for scripts that call it)
build-fast: build

## Make a commit the ckb on PATH (~/.local/bin/ckb): make deploy [REV=HEAD]
deploy:
	scripts/deploy-local.sh $(or $(REV),HEAD)

## Run all tests
test:
	go test ./...

## Lint
lint:
	golangci-lint run ./...

## Remove build artifacts
clean:
	rm -rf $(BIN_DIR)
