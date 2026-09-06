# Simple developer workflow for loom-mcp

GO ?= go
HTTP_PORT ?= 8888
LOOM_CORE_MODULE ?= github.com/CaliLuke/loom
LOOM_MCP_MODULE ?= $(LOOM_CORE_MODULE)
LOOM_CLI_PACKAGE ?= $(LOOM_CORE_MODULE)/cmd/loom
MCP_GO_SDK_VERSION ?= v1.8.0-pre.2
COVERAGE_MIN ?= 62.0
COVERAGE_RUNTIME_MIN ?= 75.0
COVERAGE_MCP_MIN ?= 78.0
COVERAGE_CODEGEN_MIN ?= 70.0
COVERAGE_TEMPORAL_MIN ?= 73.0
COVERAGE_REGISTRY_PULSE_MIN ?= 58.0
COVERAGE_PROVIDERS_MIN ?= 70.0
COVERAGE_AGENT_RUNTIME_MIN ?= 70.0
COVERAGE_MODEL_MIN ?= 70.0
COVERAGE_BEDROCK_MIN ?= 72.0
COVERAGE_GEMINI_MIN ?= 65.0
COVERAGE_DOCKER_MONGO_MIN ?= 80.0
COVERAGE_DOCKER_PULSE_MIN ?= 80.0
COVERAGE_DOCKER_REGISTRY_MIN ?= 75.0
STRESS_COUNT ?= 5
STRESS_TIMEOUT ?= 20m
FUZZ_TIME ?= 10s
# Testcontainers does not resolve Docker CLI contexts on every host. Loom MCP is
# a personal project, so default to the shared personal Colima endpoint.
DOCKER_HOST ?= $(shell command -v docker >/dev/null 2>&1 && docker context inspect colima-autok --format '{{.Endpoints.docker.Host}}' 2>/dev/null)
export DOCKER_HOST
# Every Docker-backed package owns explicit container cleanup. Disabling the
# shared cross-package Ryuk session prevents one completed package from reaping
# another package's still-running container during `go test ./...`.
TESTCONTAINERS_RYUK_DISABLED ?= true

GOPATH ?= $(shell go env GOPATH)
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)
STATICCHECK_VERSION ?= v0.8.0-rc.1
STATICCHECK := $(shell command -v staticcheck 2>/dev/null)
STATICCHECK_CHECKS ?= all,-S*,-ST*,-QF*
PROTOC := $(shell command -v protoc 2>/dev/null)
PROTOC_VERSION ?= 36.0
PROTOC_GEN_GO := protoc-gen-go
PROTOC_GEN_GO_VERSION ?= v1.36.12
PROTOC_GEN_GO_GRPC := protoc-gen-go-grpc
PROTOC_GEN_GO_GRPC_VERSION ?= v1.6.2
PROTOC_INSTALL_DIR ?= $(GOPATH)

.PHONY: all build lint lint-pre-commit lint-install-hook test test-docker coverage-check coverage-check-critical coverage-parser-test docker-coverage test-stress test-fuzz itest ci tools ensure-golangci ensure-staticcheck ensure-protoc-plugins install-protoc protoc-check run-example example-gen loom-local loom-remote loom-status update-mcp-go-sdk verify-generated verify-mcp-local regen-quickstart regen-assistant-fixture regen-progressive-discovery-fixture regen-agent-feature-fixture regen-sdkbridge-consumer-fixture verify-agent-feature-fixture

all: build lint test

build: tools
	$(GO) build ./...

lint: tools
	staticcheck -checks='$(STATICCHECK_CHECKS)' ./...
	golangci-lint run --timeout=5m

lint-pre-commit: tools
	@if [ -z "$(PATCH_FILE)" ]; then \
		echo "PATCH_FILE is required"; \
		exit 1; \
	fi
	staticcheck -checks='$(STATICCHECK_CHECKS)' ./...
	golangci-lint run --config .golangci.precommit.yml --new-from-patch "$(PATCH_FILE)" --whole-files --timeout=5m --allow-serial-runners

lint-install-hook:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit
	@echo "Installed repo hooks from .githooks"

test: tools
	LOOM_MCP_RUN_DOCKER_TESTS=0 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=0 \
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	$(GO) test -short -race -shuffle=on -covermode=atomic -coverprofile=cover.out `$(GO) list ./... | grep -v '/integration_tests'`
	$(MAKE) coverage-check
	$(MAKE) coverage-check-critical

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

coverage-check-critical: coverage-parser-test
	COVERAGE_RUNTIME_MIN=$(COVERAGE_RUNTIME_MIN) \
	COVERAGE_MCP_MIN=$(COVERAGE_MCP_MIN) \
	COVERAGE_CODEGEN_MIN=$(COVERAGE_CODEGEN_MIN) \
	COVERAGE_TEMPORAL_MIN=$(COVERAGE_TEMPORAL_MIN) \
	COVERAGE_REGISTRY_PULSE_MIN=$(COVERAGE_REGISTRY_PULSE_MIN) \
	COVERAGE_PROVIDERS_MIN=$(COVERAGE_PROVIDERS_MIN) \
	COVERAGE_AGENT_RUNTIME_MIN=$(COVERAGE_AGENT_RUNTIME_MIN) \
	COVERAGE_MODEL_MIN=$(COVERAGE_MODEL_MIN) \
	COVERAGE_BEDROCK_MIN=$(COVERAGE_BEDROCK_MIN) \
	COVERAGE_GEMINI_MIN=$(COVERAGE_GEMINI_MIN) \
	bash ./scripts/check_critical_coverage.sh cover.out

coverage-parser-test:
	bash ./scripts/check_critical_coverage_test.sh

test-docker: tools coverage-parser-test
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=1 \
	$(GO) test -tags=integration -race -shuffle=on -count=1 -covermode=atomic -coverprofile=docker-cover.out \
		./features/mongo/clientinfra \
		./features/stream/pulse/clients/pulse \
		./registry
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=1 \
	$(GO) test -C ./integration_tests/fixtures/agent_features -race -shuffle=on -count=1 \
		-run '^TestGeneratedFeatureRealTemporalMongoWorkerReplacement$$' .
	COVERAGE_DOCKER_MONGO_MIN=$(COVERAGE_DOCKER_MONGO_MIN) \
	COVERAGE_DOCKER_PULSE_MIN=$(COVERAGE_DOCKER_PULSE_MIN) \
	COVERAGE_DOCKER_REGISTRY_MIN=$(COVERAGE_DOCKER_REGISTRY_MIN) \
	bash ./scripts/check_critical_coverage.sh docker-cover.out docker

docker-coverage: test-docker

test-stress: tools
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=$(LOOM_MCP_REQUIRE_DOCKER_TESTS) \
	$(GO) test -tags=integration -race -shuffle=on -count=$(STRESS_COUNT) -timeout=$(STRESS_TIMEOUT) \
		./runtime/agent/runtime \
		./runtime/agent/model \
		./runtime/agent/completion \
		./runtime/agent/planner \
		./runtime/agent/engine/temporal \
		./features/model/anthropic \
		./features/model/bedrock \
		./features/model/codex \
		./features/model/gateway \
		./features/model/gemini \
		./features/model/middleware \
		./features/model/ollama \
		./features/model/openai \
		./runtime/toolregistry \
		./runtime/toolregistry/executor \
		./runtime/toolregistry/provider \
		./runtime/registry \
		./registry \
		./features/stream/pulse \
		./features/stream/pulse/clients/pulse
	$(GO) test -C ./integration_tests/fixtures/assistant -race -shuffle=on \
		-count=$(STRESS_COUNT) -timeout=$(STRESS_TIMEOUT) \
		-run 'TestGeneratedSDKServer(GETEnforcesSessionPrincipal|EnforcesSessionPrincipalOnEverySessionRequest|RejectsUnknownSessionIDWithNotFound|ResourceSubscriptionLifecycle|PropagatesClientCancellation)' \
		./...

test-fuzz:
	$(GO) test -race -run '^$$' -fuzz '^FuzzUnmarshalCanonicalJSON$$' -fuzztime=$(FUZZ_TIME) ./runtime/mcp
	$(GO) test -race -run '^$$' -fuzz '^FuzzDecodeFromHookInput$$' -fuzztime=$(FUZZ_TIME) ./runtime/agent/hooks
	$(GO) test -race -run '^$$' -fuzz '^FuzzProtectedRequestState$$' -fuzztime=$(FUZZ_TIME) ./runtime/mcp/sdkclient
	$(GO) test -race -run '^$$' -fuzz '^FuzzMessageJSONCodec$$' -fuzztime=$(FUZZ_TIME) ./runtime/agent/model
	$(GO) test -race -run '^$$' -fuzz '^FuzzValidateChunkShape$$' -fuzztime=$(FUZZ_TIME) ./runtime/agent/model
	$(GO) test -race -run '^$$' -fuzz '^FuzzToolFragmentPayload$$' -fuzztime=$(FUZZ_TIME) ./features/model/anthropic
	$(GO) test -race -run '^$$' -fuzz '^FuzzToolFragmentPayload$$' -fuzztime=$(FUZZ_TIME) ./features/model/bedrock
	$(GO) test -race -run '^$$' -fuzz '^FuzzParseToolArguments$$' -fuzztime=$(FUZZ_TIME) ./features/model/openai
	$(GO) test -C ./integration_tests/fixtures/assistant -race -run '^$$' \
		-fuzz '^FuzzGeneratedUnionJSONCodecs$$' -fuzztime=$(FUZZ_TIME) .

# Run generated quickstart acceptance plus every generated fixture and MCP
# framework contract. Nested fixture modules must be invoked explicitly.
itest: tools
	$(GO) test -race -count=1 ./codegen/agent/tests -run '^TestQuickstartGeneratesAndRuns$$'
	$(MAKE) verify-mcp-local

# Canonical local and hosted CI contract. Keep the recursive steps explicit so
# generation cannot race build or tests when callers enable parallel make.
ci: verify-generated
	$(MAKE) build
	$(MAKE) lint
	$(MAKE) test
	$(MAKE) itest
	$(MAKE) test-docker

tools: ensure-golangci ensure-staticcheck ensure-protoc-plugins protoc-check

ensure-golangci:
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	else \
		echo "golangci-lint found: $(GOLANGCI_LINT)"; \
	fi

ensure-staticcheck:
	@if [ -z "$(STATICCHECK)" ]; then \
		echo "Installing staticcheck $(STATICCHECK_VERSION)..."; \
		$(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION); \
	else \
		echo "staticcheck found: $(STATICCHECK)"; \
	fi

ensure-protoc-plugins:
	@want="$$(printf '%s' '$(PROTOC_GEN_GO_VERSION)' | sed 's/^v//')"; \
	got="$$(command -v $(PROTOC_GEN_GO) >/dev/null 2>&1 && $(PROTOC_GEN_GO) --version | awk '{print $$NF}' | sed 's/^v//' || true)"; \
	if [ "$$got" != "$$want" ]; then \
		echo "Installing protoc-gen-go $(PROTOC_GEN_GO_VERSION)..."; \
		$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION); \
	else \
		echo "protoc-gen-go $$got found at: $$(command -v $(PROTOC_GEN_GO))"; \
	fi
	@want="$$(printf '%s' '$(PROTOC_GEN_GO_GRPC_VERSION)' | sed 's/^v//')"; \
	got="$$(command -v $(PROTOC_GEN_GO_GRPC) >/dev/null 2>&1 && $(PROTOC_GEN_GO_GRPC) --version | awk '{print $$NF}' | sed 's/^v//' || true)"; \
	if [ "$$got" != "$$want" ]; then \
		echo "Installing protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)..."; \
		$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION); \
	else \
		echo "protoc-gen-go-grpc $$got found at: $$(command -v $(PROTOC_GEN_GO_GRPC))"; \
	fi

install-protoc:
	bash ./scripts/install_protoc.sh "$(PROTOC_VERSION)" "$(PROTOC_INSTALL_DIR)"

protoc-check:
	@if [ -z "$(PROTOC)" ]; then \
		echo "Error: protoc $(PROTOC_VERSION) is not installed or not in PATH."; \
		echo "Run 'make install-protoc' and add $(PROTOC_INSTALL_DIR)/bin to PATH."; \
		exit 1; \
	fi; \
	actual="$$($(PROTOC) --version | awk '{print $$2}')"; \
	if [ "$$actual" != "$(PROTOC_VERSION)" ]; then \
		echo "Error: protoc $$actual found at $(PROTOC); expected $(PROTOC_VERSION)."; \
		echo "Run 'make install-protoc' and put $(PROTOC_INSTALL_DIR)/bin before the current protoc on PATH."; \
		exit 1; \
	fi; \
	echo "protoc $$actual found at: $(PROTOC)"

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
	cd ./integration_tests/fixtures/agent_features && GOWORK=off $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	cd ./quickstart && GOWORK=off $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	$(GO) work edit -replace github.com/modelcontextprotocol/go-sdk=github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	$(GO) mod tidy
	cd ./integration_tests/fixtures/assistant && $(GO) mod tidy
	cd ./integration_tests/fixtures/agent_features && GOWORK=off $(GO) mod tidy
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) mod tidy
	cd ./quickstart && GOWORK=off $(GO) mod tidy
	cd ./quickstart && GOWORK=off $(GO) list -mod=mod -deps github.com/CaliLuke/loom/cmd/loom >/dev/null

verify-generated: tools
	bash ./scripts/verify_generated.sh

verify-mcp-local:
	$(GO) test -C ./integration_tests/fixtures/assistant -race ./... -count=1
	$(GO) test -C ./integration_tests/fixtures/agent_features -race ./... -count=1
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) test -race ./... -count=1
	$(GO) test -race -count=1 ./integration_tests/framework

regen-quickstart:
	cd ./quickstart && GOWORK=off $(GO) run -mod=mod $(LOOM_CLI_PACKAGE) gen example.com/quickstart/design

regen-assistant-fixture:
	cd ./integration_tests/fixtures/assistant && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/assistant/design

regen-progressive-discovery-fixture:
	cd ./integration_tests/fixtures/assistant && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/assistant/progressive_discovery/design -o progressive_discovery

regen-agent-feature-fixture:
	cd ./integration_tests/fixtures/agent_features && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/agentfeatures/design

regen-sdkbridge-consumer-fixture:
	bash ./scripts/sdkbridge_consumer_guard.sh regen
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) list -mod=mod ./design >/dev/null
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) run -mod=mod $(LOOM_CLI_PACKAGE) gen example.com/sdkbridgeconsumer/design
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) mod tidy

verify-agent-feature-fixture:
	$(GO) test -C ./integration_tests/fixtures/agent_features -race ./... -count=1
