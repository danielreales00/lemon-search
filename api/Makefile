.PHONY: help fmt lint test test-integration test-e2e cover-gate build build-api build-ingest tidy install-tools

GO ?= go
GOFUMPT ?= gofumpt
GOLANGCI_LINT ?= golangci-lint

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

fmt: ## Format code with gofumpt
	$(GOFUMPT) -w .

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

test: ## Run unit tests with coverage (pure, no DB)
	$(GO) test -race -coverprofile=coverage.out ./...

test-integration: ## Run integration tests (needs a live DB: make db-up && make db-reset)
	$(GO) test -race -tags=integration ./...

test-e2e: ## Run e2e tests (needs a live DB: make db-up && make db-reset)
	$(GO) test -race -tags=e2e ./...

cover-gate: ## Enforce >=90% statement coverage on core pkgs (rank/intent/config)
	../scripts/ci/coverage-gate.sh

build: build-api build-ingest ## Build both binaries

build-api: ## Build the API binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o bin/api ./cmd/api

build-ingest: ## Build the ingestion CLI
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o bin/ingest ./cmd/ingest

tidy: ## Ensure go.mod is tidy
	$(GO) mod tidy

install-tools: ## Install dev tooling (gofumpt, golangci-lint)
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
