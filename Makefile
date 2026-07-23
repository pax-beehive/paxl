GO ?= go
GO_BIN := $(shell $(GO) env GOBIN)
ifeq ($(GO_BIN),)
GO_BIN := $(shell $(GO) env GOPATH)/bin
endif

GOLANGCI_LINT ?= $(GO_BIN)/golangci-lint
GOIMPORTS ?= $(GO_BIN)/goimports
GOLINES ?= $(GO_BIN)/golines
MOCKERY ?= $(GO_BIN)/mockery
GOBCO ?= $(GO_BIN)/gobco
GOCACHE ?= /tmp/paxl-go-cache
GOLANGCI_LINT_CACHE ?= /tmp/paxl-golangci-lint-cache
COVERAGE_MIN ?= 80
MUTATION_TARGETS ?= ./internal/model/store
MUTATION_TIMEOUT ?= 30
MUTATION_FLAGS ?=
COGNITIVE_TARGETS ?= .
COGNITIVE_TOP ?= 20
COGNITIVE_FLAGS ?= -top $(COGNITIVE_TOP) -avg
RELEASE_VERSION ?= patch
RELEASE_TAGS ?= stable

GO_PACKAGES := ./...
GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')

.PHONY: lint format format-check test test-cover onprem-channel-e2e branch-cover branch-cover-install mutation-test cognitive-complexity release-paxl release-paxl-dry-run mock gen

lint:
	GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) GOCACHE=$(GOCACHE) $(GOLANGCI_LINT) run $(GO_PACKAGES)

format:
	@if [ -z "$(GO_FILES)" ]; then \
		echo "No Go files to format."; \
	else \
		gofmt -w $(GO_FILES); \
		$(GOIMPORTS) -w $(GO_FILES); \
		$(GOLINES) -w $(GO_FILES); \
	fi

format-check:
	@if [ -z "$(GO_FILES)" ]; then \
		echo "No Go files to check."; \
	else \
		unformatted="$$(gofmt -l $(GO_FILES))"; \
		if [ -n "$$unformatted" ]; then \
			echo "Files need gofmt:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi; \
		unformatted="$$( $(GOIMPORTS) -l $(GO_FILES) )"; \
		if [ -n "$$unformatted" ]; then \
			echo "Files need goimports:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi; \
		unformatted="$$( $(GOLINES) --dry-run -l $(GO_FILES) )"; \
		if [ -n "$$unformatted" ]; then \
			echo "Files need golines:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi; \
	fi

test:
	GOCACHE=$(GOCACHE) $(GO) test -count=1 $(GO_PACKAGES)

test-cover:
	GOCACHE=$(GOCACHE) $(GO) test -count=1 -covermode=atomic -coverprofile=coverage.out $(GO_PACKAGES)
	@GOCACHE=$(GOCACHE) $(GO) tool cover -func=coverage.out | awk -v min="$(COVERAGE_MIN)" '/^total:/ { \
		gsub(/%/, "", $$3); \
		if (($$3 + 0) < (min + 0)) { \
			printf "Coverage %.1f%% is below %.1f%%.\n", $$3, min; \
			exit 1; \
		} \
		printf "Coverage %.1f%% meets %.1f%%.\n", $$3, min; \
	}'

onprem-channel-e2e:
	./scripts/onprem-channel-e2e.sh

branch-cover:
	@if [ ! -x "$(GOBCO)" ]; then \
		echo "Missing gobco at $(GOBCO)."; \
		echo "Install it with: go install github.com/rillig/gobco@latest"; \
		exit 1; \
	fi
	GOCACHE=$(GOCACHE) ./scripts/branch_coverage.sh "$(GOBCO)"

branch-cover-install:
	GOCACHE=$(GOCACHE) $(GO) install github.com/rillig/gobco@latest

mutation-test:
	GOCACHE=$(GOCACHE) $(GO) tool go-mutesting --exec-timeout=$(MUTATION_TIMEOUT) $(MUTATION_FLAGS) $(MUTATION_TARGETS)

cognitive-complexity:
	GOCACHE=$(GOCACHE) $(GO) tool gocognit $(COGNITIVE_FLAGS) $(COGNITIVE_TARGETS)

release-paxl:
	scripts/release_paxl.sh $(RELEASE_VERSION) $(RELEASE_TAGS)

release-paxl-dry-run:
	PAX_RELEASE_DRY_RUN=1 scripts/release_paxl.sh $(RELEASE_VERSION) $(RELEASE_TAGS)

mock:
	GOCACHE=$(GOCACHE) $(MOCKERY) --config .mockery.yaml

gen:
	GOCACHE=$(GOCACHE) $(GO) generate $(GO_PACKAGES)
