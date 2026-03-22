# Simple developer workflow for loom-mcp

GO ?= go
HTTP_PORT ?= 8888
LOOM_CORE_MODULE ?= github.com/CaliLuke/loom
LOOM_MCP_MODULE ?= $(LOOM_CORE_MODULE)
LOOM_CLI_PACKAGE ?= $(LOOM_CORE_MODULE)/cmd/loom

GOPATH ?= $(shell go env GOPATH)
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)
PROTOC := $(shell command -v protoc 2>/dev/null)
PROTOC_GEN_GO := protoc-gen-go
PROTOC_GEN_GO_GRPC := protoc-gen-go-grpc

.PHONY: all build lint lint-pre-commit lint-install-hook test itest ci tools ensure-golangci ensure-protoc-plugins protoc-check run-example example-gen goa-local goa-remote goa-status verify-mcp-local regen-assistant-fixture

all: build lint test

build: tools
	$(GO) build ./...

lint: tools
	golangci-lint run --timeout=5m

lint-pre-commit: tools
	@if [ -z "$(PATCH_FILE)" ]; then \
		echo "PATCH_FILE is required"; \
		exit 1; \
	fi
	golangci-lint run --config .golangci.precommit.yml --new-from-patch "$(PATCH_FILE)" --whole-files --timeout=5m --allow-serial-runners

lint-install-hook:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit
	@echo "Installed repo hooks from .githooks"

test: tools
	$(GO) test -race -covermode=atomic -coverprofile=cover.out `$(GO) list ./... | grep -v '/integration_tests'`

# Run integration tests (scenarios under integration_tests/)
itest: tools
	$(GO) test -race -vet=off -parallel 1 ./integration_tests/...

ci: build lint test

tools: ensure-golangci ensure-protoc-plugins protoc-check

ensure-golangci:
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "Installing golangci-lint v2.6.2..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOPATH)/bin v2.6.2; \
	else \
		echo "golangci-lint found: $(GOLANGCI_LINT)"; \
	fi

ensure-protoc-plugins:
	@if ! command -v $(PROTOC_GEN_GO) >/dev/null 2>&1; then \
		echo "Installing protoc-gen-go (latest)..."; \
		$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest; \
	else \
		echo "protoc-gen-go found at: $$(command -v $(PROTOC_GEN_GO))"; \
	fi
	@if ! command -v $(PROTOC_GEN_GO_GRPC) >/dev/null 2>&1; then \
		echo "Installing protoc-gen-go-grpc (latest)..."; \
		$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest; \
	else \
		echo "protoc-gen-go-grpc found at: $$(command -v $(PROTOC_GEN_GO_GRPC))"; \
	fi

protoc-check:
	@if [ -z "$(PROTOC)" ]; then \
		echo "Error: protoc is not installed or not in PATH."; \
		echo "Install via your package manager (e.g., 'brew install protobuf' or 'apt-get install protobuf-compiler')."; \
		exit 1; \
	fi

run-example:
	cd example/complete && $(GO) run ./cmd/orchestrator --http-port $(HTTP_PORT)

gen-example:
	cd example/complete && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/assistant/design

gen-registry:
	$(GO) run $(LOOM_CLI_PACKAGE) gen github.com/CaliLuke/loom-mcp/registry/design -o registry

goa-local:
	bash ./scripts/goa_core_mode.sh local

goa-remote:
	bash ./scripts/goa_core_mode.sh remote

goa-status:
	bash ./scripts/goa_core_mode.sh status

verify-mcp-local:
	go test -C ./integration_tests/fixtures/assistant ./... -count=1
	go test ./integration_tests/framework -count=1

regen-assistant-fixture:
	cd ./integration_tests/fixtures/assistant && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/assistant/design
