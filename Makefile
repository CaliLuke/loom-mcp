# Simple developer workflow for loom-mcp

GO ?= go
HTTP_PORT ?= 8888
LOOM_CORE_MODULE ?= github.com/CaliLuke/loom
LOOM_MCP_MODULE ?= $(LOOM_CORE_MODULE)
LOOM_CLI_PACKAGE ?= $(LOOM_CORE_MODULE)/cmd/loom
MCP_GO_SDK_VERSION ?= v1.7.0
COVERAGE_MIN ?= 62.0

GOPATH ?= $(shell go env GOPATH)
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)
PROTOC := $(shell command -v protoc 2>/dev/null)
PROTOC_GEN_GO := protoc-gen-go
PROTOC_GEN_GO_GRPC := protoc-gen-go-grpc

.PHONY: all build lint lint-pre-commit lint-install-hook test coverage-check itest ci tools ensure-golangci ensure-protoc-plugins protoc-check run-example example-gen loom-local loom-remote loom-status update-mcp-go-sdk verify-mcp-local regen-quickstart regen-assistant-fixture regen-progressive-discovery-fixture regen-agent-feature-fixture verify-agent-feature-fixture

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
	$(GO) test -short -race -shuffle=on -covermode=atomic -coverprofile=cover.out `$(GO) list ./... | grep -v '/integration_tests'`
	$(MAKE) coverage-check

coverage-check:
	@coverage=$$($(GO) tool cover -func=cover.out | awk '/^total:/ { gsub("%", "", $$3); print $$3 }'); \
	if [ -z "$$coverage" ]; then \
		echo "coverage total missing from cover.out"; \
		exit 1; \
	fi; \
	awk -v actual="$$coverage" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
		if (actual + 0 < minimum + 0) { \
			printf "coverage %.1f%% is below required %.1f%%\n", actual, minimum; \
			exit 1; \
		} \
		printf "coverage %.1f%% meets required %.1f%%\n", actual, minimum; \
	}'

# Run integration tests (scenarios under integration_tests/)
itest: tools
	$(GO) test -race -count=1 ./codegen/agent/tests -run '^TestQuickstartGeneratesAndRuns$$'
	$(GO) test -C ./integration_tests/fixtures/assistant ./... -count=1
	$(GO) test -C ./integration_tests/fixtures/agent_features ./... -count=1
	MCP_CLI_TESTS=true $(GO) test -race -vet=off -count=1 ./integration_tests/...

ci: build lint test

tools: ensure-golangci ensure-protoc-plugins protoc-check

ensure-golangci:
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "Installing golangci-lint latest..."; \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest; \
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
	$(GO) run $(LOOM_CLI_PACKAGE) gen github.com/CaliLuke/loom-mcp/v2/registry/design -o registry

loom-local:
	bash ./scripts/loom_core_mode.sh local

loom-remote:
	bash ./scripts/loom_core_mode.sh remote

loom-status:
	bash ./scripts/loom_core_mode.sh status

update-mcp-go-sdk:
	$(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	cd ./integration_tests/fixtures/assistant && $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	$(GO) work edit -replace github.com/modelcontextprotocol/go-sdk=github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	$(GO) mod tidy
	cd ./integration_tests/fixtures/assistant && $(GO) mod tidy

verify-mcp-local:
	go test -C ./integration_tests/fixtures/assistant ./... -count=1
	go test -C ./integration_tests/fixtures/agent_features ./... -count=1
	go test ./integration_tests/framework -count=1

regen-quickstart:
	cd ./quickstart && GOWORK=off $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/quickstart/design

regen-assistant-fixture:
	cd ./integration_tests/fixtures/assistant && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/assistant/design

regen-progressive-discovery-fixture:
	cd ./integration_tests/fixtures/assistant && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/assistant/progressive_discovery/design -o progressive_discovery

regen-agent-feature-fixture:
	cd ./integration_tests/fixtures/agent_features && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/agentfeatures/design

verify-agent-feature-fixture:
	go test -C ./integration_tests/fixtures/agent_features ./... -count=1
