# Simple developer workflow for loom-mcp

GO ?= go
HTTP_PORT ?= 8888
LOOM_CORE_MODULE ?= github.com/CaliLuke/loom
LOOM_MCP_MODULE ?= $(LOOM_CORE_MODULE)
LOOM_CLI_PACKAGE ?= $(LOOM_CORE_MODULE)/cmd/loom
MCP_GO_SDK_VERSION ?= v1.8.1-0.20261016161459-3e9d812b1814
COVERAGE_MIN ?= 62.0
FAST_COVERAGE_MIN ?= 90.1
COVERAGE_PACKAGES ?= github.com/CaliLuke/loom-mcp/v2/...
COVERAGE_DIR ?= $(CURDIR)/.cache/coverage-fast
FAST_TEST_FLAGS ?= -race -count=1
# Each independently compiled fixture gets its own profile; only framework code
# is instrumented, so fixture implementation cannot inflate framework coverage.
fast_cover_args = $(if $(FAST_COVERAGE_DIR),-covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) -coverprofile=$(abspath $(FAST_COVERAGE_DIR))/$(1).out)
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
GOLANGCI_LINT_VERSION ?= v2.14.0
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)
# Cached diagnostics retain source paths. Sharing them across worktrees can
# bypass anchored path exclusions by reporting files from a sibling checkout.
GOLANGCI_LINT_CACHE ?= $(CURDIR)/.cache/golangci-lint
export GOLANGCI_LINT_CACHE
STATICCHECK_VERSION ?= v0.8.1
STATICCHECK_TOOLS_VERSION ?= v0.51.0
STATICCHECK_CHECKS ?= all,-S*,-ST*,-QF*
PROTOC := $(shell command -v protoc 2>/dev/null)
PROTOC_VERSION ?= 36.2
PROTOC_GEN_GO := protoc-gen-go
PROTOC_GEN_GO_VERSION ?= v1.36.12
PROTOC_GEN_GO_GRPC := protoc-gen-go-grpc
PROTOC_GEN_GO_GRPC_VERSION ?= v1.6.2
PROTOC_INSTALL_DIR ?= $(GOPATH)

.PHONY: all build lint lint-pre-commit lint-install-hook test test-docker coverage-check coverage-check-critical coverage-parser-test docker-coverage test-stress test-fuzz itest ci tools ensure-golangci ensure-staticcheck ensure-protoc-plugins install-protoc protoc-check gen-example loom-local loom-remote loom-status update-mcp-go-sdk verify-generated verify-mcp-local regen-quickstart regen-assistant-fixture regen-longrun-fixture regen-progressive-discovery-fixture regen-agent-feature-fixture regen-sdkbridge-consumer-fixture verify-agent-feature-fixture

all: build lint test

.PHONY: release release-promote release-test release-model-test context-model-test a2a-model-test
release:
	$(GO) run ./internal/cmd/release --mode alpha --source "$$SOURCE" --version "$$VERSION"

release-promote:
	$(GO) run ./internal/cmd/release --mode promote --alpha "$$ALPHA" --version "$$VERSION"

release-test:
	$(GO) test -race -count=1 ./internal/release ./internal/cmd/release

# TLC is a development-only tool; supply the downloaded tla2tools.jar path.
release-model-test:
	bash ./scripts/release_model_test.sh "$(TLA2TOOLS_JAR)"

# TLA+ model of the context-compaction design (docs/formal/ContextCompaction.md).
context-model-test:
	bash ./scripts/context_model_test.sh "$(TLA2TOOLS_JAR)"

# Bounded A2A admission, cancellation and receipt safety (docs/formal/A2AConcurrency.md).
a2a-model-test:
	bash ./scripts/a2a_model_test.sh "$(TLA2TOOLS_JAR)"

build: tools
	$(GO) build ./...

lint: tools
	staticcheck -checks='$(STATICCHECK_CHECKS)' ./...
	golangci-lint run --timeout=5m --allow-serial-runners

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

# Keep race instrumentation out of atomic coverage counters in numeric loops.
# Both passes execute the complete short unit suite with the same exclusions.
.PHONY: test-race test-coverage
test: tools
	$(MAKE) test-race
	$(MAKE) test-coverage
	$(MAKE) test-codex-live

test-race: tools
	LOOM_MCP_RUN_DOCKER_TESTS=0 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=0 \
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	$(GO) test -short -race -shuffle=on -timeout=30m `$(GO) list ./... | grep -v '/integration_tests'`

test-coverage: tools
	mkdir -p "$(COVERAGE_DIR)"
	LOOM_MCP_RUN_DOCKER_TESTS=0 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=0 \
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	$(GO) test -short -shuffle=on -timeout=30m -covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) -coverprofile="$(COVERAGE_DIR)/unit.raw" `$(GO) list ./... | grep -v '/integration_tests'`
	bash scripts/merge_coverage.sh "$(COVERAGE_DIR)/unit.raw" > cover.out
	$(MAKE) coverage-check
	$(MAKE) coverage-check-critical

# Full payload and cache-growth acceptance; part of exhaustive qualification.
.PHONY: test-payload-growth
test-payload-growth:
	$(GO) test -count=1 -timeout=20m -v ./runtime/agent/runtime -run '^(TestIndexPayloadAcceptance|TestIndexLoaderGrowth|TestPlanActivityInputFixedFields|TestCallRecordGrowth)$$'
	$(GO) test -count=1 -timeout=3m -v ./integration_tests/fixtures/agent_features -run '^TestGeneratedFeatureRealTemporalContracts/RunRecord_survives_abandoned_history$$'

# Run on every local make test, even when Go can reuse cached unit results.
.PHONY: test-codex-live
test-codex-live:
	$(GO) test -count=1 -v -timeout=3m ./features/model/codex -run '^TestLiveCodex$$'

PROFILE ?= cover.out

coverage-check:
	@coverage=$$($(GO) tool cover -func="$(PROFILE)" | awk '/^total:/ { gsub("%", "", $$3); print $$3 }'); \
	if [ -z "$$coverage" ]; then \
		echo "coverage total missing from $(PROFILE)"; \
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
	mkdir -p "$(COVERAGE_DIR)"
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=1 \
	$(GO) test -tags=integration -race -shuffle=on -count=1 -covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) -coverprofile="$(COVERAGE_DIR)/docker.raw" \
		./features/mongo/clientinfra \
		./features/stream/pulse/clients/pulse \
		./registry
	bash scripts/merge_coverage.sh "$(COVERAGE_DIR)/docker.raw" > docker-cover.out
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=1 \
	$(GO) test -C ./integration_tests/fixtures/agent_features -race -shuffle=on -count=1 \
		-covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) -coverprofile="$(COVERAGE_DIR)/worker-replacement.out" \
		-run '^TestGeneratedFeatureRealTemporalMongoWorkerReplacement$$' .
	COVERAGE_DOCKER_MONGO_MIN=$(COVERAGE_DOCKER_MONGO_MIN) \
	COVERAGE_DOCKER_PULSE_MIN=$(COVERAGE_DOCKER_PULSE_MIN) \
	COVERAGE_DOCKER_REGISTRY_MIN=$(COVERAGE_DOCKER_REGISTRY_MIN) \
	bash ./scripts/check_critical_coverage.sh docker-cover.out docker

docker-coverage: test-docker

test-stress: tools
	# itest retains full payload and cache-growth acceptance.
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=$(LOOM_MCP_REQUIRE_DOCKER_TESTS) \
	$(GO) test -short -tags=integration -race -shuffle=on -count=$(STRESS_COUNT) -timeout=$(STRESS_TIMEOUT) \
		./runtime/agent/runtime
	TESTCONTAINERS_RYUK_DISABLED=$(TESTCONTAINERS_RYUK_DISABLED) \
	LOOM_MCP_RUN_DOCKER_TESTS=1 \
	LOOM_MCP_REQUIRE_DOCKER_TESTS=$(LOOM_MCP_REQUIRE_DOCKER_TESTS) \
	$(GO) test -tags=integration -race -shuffle=on -count=$(STRESS_COUNT) -timeout=$(STRESS_TIMEOUT) \
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

# Run engine, payload, recall, and generated quickstart acceptance plus every
# generated fixture and MCP framework contract. Invoke nested modules explicitly.
itest: tools
	$(MAKE) test-engine-conformance
	$(MAKE) test-payload-growth
	$(MAKE) test-recall-growth
	$(GO) test $(FAST_TEST_FLAGS) $(call fast_cover_args,quickstart) ./codegen/agent/tests -run '^TestQuickstartGeneratesAndRuns$$'
	$(MAKE) verify-mcp-local

# Routine CI retains all short unit/coverage and generated integration contracts.
# Hosted CI runs these five lanes on separate runners; local execution is serial.
.PHONY: ci-static ci-race ci-coverage ci-integration ci-docker qualification test-recall-growth test-longrun verify-mcp-fixtures
ci: ci-static
	$(MAKE) ci-race
	$(MAKE) ci-coverage
	$(MAKE) ci-integration
	$(MAKE) ci-docker
	$(MAKE) coverage-fast-check

ci-static: verify-generated
	$(MAKE) release-test
	$(MAKE) build
	$(MAKE) lint

ci-race:
	$(MAKE) test-race
	$(MAKE) test-codex-live

ci-coverage:
	$(MAKE) test-coverage
	$(MAKE) coverage-integration

ci-integration: tools
	$(MAKE) test-engine-conformance
	$(GO) test $(FAST_TEST_FLAGS) $(call fast_cover_args,quickstart) ./codegen/agent/tests -run '^TestQuickstartGeneratesAndRuns$$'
	$(MAKE) verify-mcp-fixtures

ci-docker:
	$(MAKE) test-docker

# Reuse the same fast integration entry point without race instrumentation for
# measurement. The separate integration job still requires its race-enabled run.
.PHONY: coverage-integration coverage-fast-check coverage-merge-test
coverage-integration: tools
	mkdir -p "$(COVERAGE_DIR)"
	$(MAKE) ci-integration FAST_TEST_FLAGS=-count=1 FAST_COVERAGE_DIR="$(COVERAGE_DIR)"

coverage-merge-test:
	bash scripts/merge_coverage_test.sh

coverage-fast-check: coverage-merge-test
	bash scripts/merge_coverage.sh cover.out docker-cover.out \
		"$(COVERAGE_DIR)/engine.out" "$(COVERAGE_DIR)/quickstart.out" \
		"$(COVERAGE_DIR)/assistant.out" "$(COVERAGE_DIR)/agent-features.out" \
		"$(COVERAGE_DIR)/sdkbridge.out" "$(COVERAGE_DIR)/framework.out" \
		"$(COVERAGE_DIR)/worker-replacement.out" > "$(COVERAGE_DIR)/combined.out"
	$(MAKE) coverage-check PROFILE="$(COVERAGE_DIR)/combined.out" COVERAGE_MIN=$(FAST_COVERAGE_MIN)

# Exhaustive acceptance is explicit and scheduled, not a per-push release gate.
# Keep the full workloads unchanged and visible as separate workflow steps.
qualification: tools
	$(MAKE) test-timing-qualification
	$(MAKE) test-payload-growth
	$(MAKE) test-recall-growth
	$(MAKE) test-longrun

.PHONY: test-timing-qualification
test-timing-qualification:
	LOOM_MCP_TIMING_QUALIFICATION=1 $(GO) test -C ./integration_tests/fixtures/agent_features -race -count=1 -timeout=5m -v -run '^TestGeneratedFeatureRealTemporalContracts/(transfer_dispatch_avoids_poll_throttle|active_budget_excludes_external_waits)$$' .

test-recall-growth:
	$(GO) test -race -count=1 -timeout=30m ./runtime/agent/transcript/recall/ -run '^TestRecallScript1000Steps$$'

test-longrun:
	$(GO) test -C ./integration_tests/fixtures/longrun -p=1 -race -count=1 -timeout=90m -v ./...

tools: ensure-golangci ensure-staticcheck ensure-protoc-plugins protoc-check

ensure-golangci:
	@if [ -z "$(GOLANGCI_LINT)" ] || [ "$$(golangci-lint version --short)" != "$$(printf '%s' '$(GOLANGCI_LINT_VERSION)' | sed 's/^v//')" ]; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	else \
		echo "golangci-lint found: $(GOLANGCI_LINT)"; \
	fi

ensure-staticcheck:
	@GO="$(GO)" bash ./scripts/install_staticcheck.sh "$(STATICCHECK_VERSION)" "$(STATICCHECK_TOOLS_VERSION)"

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

gen-example: regen-assistant-fixture

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
	cd ./integration_tests/fixtures/longrun && $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	cd ./integration_tests/fixtures/agent_features && GOWORK=off $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	cd ./quickstart && GOWORK=off $(GO) get github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	$(GO) work edit -replace github.com/modelcontextprotocol/go-sdk=github.com/modelcontextprotocol/go-sdk@$(MCP_GO_SDK_VERSION)
	$(GO) mod tidy
	cd ./integration_tests/fixtures/assistant && $(GO) mod tidy
	cd ./integration_tests/fixtures/longrun && $(GO) mod tidy
	cd ./integration_tests/fixtures/agent_features && GOWORK=off $(GO) mod tidy
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) mod tidy
	cd ./quickstart && GOWORK=off $(GO) mod tidy
	cd ./quickstart && GOWORK=off $(GO) list -mod=mod -deps github.com/CaliLuke/loom/cmd/loom >/dev/null

verify-generated: tools
	bash ./scripts/verify_generated.sh

verify-mcp-local:
	$(MAKE) test-longrun
	$(MAKE) verify-mcp-fixtures

verify-mcp-fixtures:
	$(GO) test -C ./integration_tests/fixtures/assistant $(FAST_TEST_FLAGS) $(call fast_cover_args,assistant) ./...
	$(GO) test -C ./integration_tests/fixtures/agent_features $(FAST_TEST_FLAGS) $(call fast_cover_args,agent-features) ./...
	cd ./integration_tests/fixtures/sdkbridge_consumer && GOWORK=off $(GO) test $(FAST_TEST_FLAGS) $(call fast_cover_args,sdkbridge) ./...
	$(GO) test $(FAST_TEST_FLAGS) $(call fast_cover_args,framework) ./integration_tests/framework

regen-quickstart:
	cd ./quickstart && GOWORK=off $(GO) run -mod=mod $(LOOM_CLI_PACKAGE) gen example.com/quickstart/design

regen-longrun-fixture:
	cd ./integration_tests/fixtures/longrun && $(GO) run $(LOOM_CLI_PACKAGE) gen example.com/longrun/design

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
	$(GO) test -C ./integration_tests/fixtures/agent_features $(FAST_TEST_FLAGS) $(call fast_cover_args,agent-features) ./...

# Real backend conformance includes Temporal worker replacement and retries.
.PHONY: test-engine-conformance
test-engine-conformance:
	$(GO) test $(FAST_TEST_FLAGS) $(call fast_cover_args,engine) -timeout=5m -v ./runtime/agent/engine/inmem ./runtime/agent/engine/temporal ./runtime/agent/engine/selvedge -run '^TestConformance$$'

.PHONY: selvedge-local selvedge-remote selvedge-status test-engine-conformance-postgres
selvedge-local:
	./scripts/selvedge_mode.sh local

selvedge-remote:
	./scripts/selvedge_mode.sh remote

selvedge-status:
	./scripts/selvedge_mode.sh status

# PostgreSQL conformance is opt-in until the integration matrix includes it.
test-engine-conformance-postgres:
	LOOM_MCP_RUN_DOCKER_TESTS=1 LOOM_MCP_REQUIRE_DOCKER_TESTS=1 $(GO) test -race -count=1 -timeout=5m -v ./runtime/agent/engine/selvedge -run '^TestConformancePostgres$$'
