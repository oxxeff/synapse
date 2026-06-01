# Synapse - development pipeline.
# Single entry point for local and CI runs: targets are named after Jenkinsfile
# stages so CI and local builds go through the same path.

MODULE  := go.oxef.dev/ci/synapse
BIN_DIR := dist
BIN     := $(BIN_DIR)/synapse
CMD     := ./cmd/synapse

GO       ?= go
GOLANGCI ?= golangci-lint

# In CI the build runs from a workspace owned by another user, so go refuses to
# query the VCS (dubious ownership). Disable buildvcs; identity comes via ldflags.
export GOFLAGS ?= -buildvcs=false

# Build identity is injected into the binary via ldflags of the version package.
# VERSION is the human-managed source of truth (root VERSION file); a release tag
# must match it. BUILD is the CI build number (Jenkins provides BUILD_NUMBER); 0 locally.
VERSION ?= $(shell cat $(CURDIR)/VERSION 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD   ?= $(or $(BUILD_NUMBER),0)

LDFLAGS := -s -w \
  -X $(MODULE)/internal/version.Version=$(VERSION) \
  -X $(MODULE)/internal/version.Commit=$(COMMIT) \
  -X $(MODULE)/internal/version.Date=$(DATE) \
  -X $(MODULE)/internal/version.Build=$(BUILD)

# Starting coverage floor. Ratcheted up as the code grows; silent lowering is
# forbidden - change it only by a deliberate edit of this line.
MIN_COVERAGE ?= 70

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the binary into dist/ with build identity
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(CMD)

.PHONY: run
run: ## Run from sources
	$(GO) run $(CMD)

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	$(GO) mod tidy

.PHONY: fmt
fmt: ## Format and auto-fix (gofumpt + goimports + autofix)
	$(GOLANGCI) run --fix ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: lint-fast
lint-fast: ## Fast linter run (pre-commit)
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { echo "golangci-lint not found: https://golangci-lint.run/welcome/install/"; exit 1; }
	$(GOLANGCI) run --fast ./...

.PHONY: lint-full
lint-full: ## Full linter run (before a PR, in CI)
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { echo "golangci-lint not found: https://golangci-lint.run/welcome/install/"; exit 1; }
	$(GOLANGCI) run ./...

# Separate Jenkinsfile stage: the nil-dereference analyzer complements the linter
# with interprocedural analysis that golangci-lint does not do.
.PHONY: nilcheck
nilcheck: ## Nil-dereference check (nilaway)
	@command -v nilaway >/dev/null 2>&1 || { echo "nilaway not found: go install go.uber.org/nilaway/cmd/nilaway@latest"; exit 1; }
	nilaway ./...

.PHONY: test
test: ## Unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ## Unit tests with the race detector
	$(GO) test -race ./...

# Runs over ./..., but coverage counts only business logic in internal/: cmd/ is a
# thin entrypoint without logic, its 0% must not skew the coverage ratchet.
# Artifacts under dist/ are picked up by Jenkins (JUnit + HTML coverage report).
COVERPROFILE := dist/coverage/coverage.out

.PHONY: test-ci
test-ci: ## CI tests: race + coverage + JUnit (gotestsum), with a coverage gate
	@mkdir -p dist/coverage
	@command -v gotestsum >/dev/null 2>&1 || { echo "gotestsum not found: go install gotest.tools/gotestsum@latest"; exit 1; }
	gotestsum --junitfile dist/test-results.xml --format testname -- \
		-race -count=1 -covermode=atomic -coverpkg=./internal/... -coverprofile=$(COVERPROFILE) ./...
	$(GO) tool cover -html=$(COVERPROFILE) -o dist/coverage/coverage.html
	@$(MAKE) --no-print-directory coverage-gate

.PHONY: coverage-gate
coverage-gate: ## Check coverage against MIN_COVERAGE
	@total=$$($(GO) tool cover -func=$(COVERPROFILE) | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	echo "coverage: $$total% (minimum $(MIN_COVERAGE)%)"; \
	awk -v t="$$total" -v m="$(MIN_COVERAGE)" 'BEGIN{ exit (t+0 < m+0) }' || \
	  { echo "coverage $$total% below minimum $(MIN_COVERAGE)%"; exit 1; }

# Integration tests are behind the integration build tag so the unit run skips them.
.PHONY: test-integration
test-integration: ## Integration tests (integration build tag)
	$(GO) test -tags=integration -race ./...

# Release dry-run via goreleaser: builds the binary and image locally without
# publishing, to verify the build and identity injection.
.PHONY: release-snapshot
release-snapshot: ## Release dry-run via goreleaser: binary and image, no publish
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not found; version pinned in .goreleaser-version"; exit 1; }
	BUILD=$(BUILD) goreleaser release --snapshot --clean --skip=publish

.PHONY: check-pr
check-pr: lint-full nilcheck test-ci ## Full gate before a PR
	@echo "check-pr: ok"

.PHONY: setup-hooks
setup-hooks: ## Activate git hooks from .githooks (pre-commit)
	git config core.hooksPath .githooks
	@echo "core.hooksPath = .githooks"

.PHONY: tools
tools: ## Install helper tooling (gotestsum, nilaway)
	$(GO) install gotest.tools/gotestsum@latest
	$(GO) install go.uber.org/nilaway/cmd/nilaway@latest

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -rf $(BIN_DIR)
