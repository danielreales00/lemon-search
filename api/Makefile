.PHONY: help fmt lint test build build-api build-ingest tidy install-tools

GO ?= go
GOFUMPT ?= gofumpt
GOLANGCI_LINT ?= golangci-lint

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

fmt: ## Format code with gofumpt
	$(GOFUMPT) -w .

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

test: ## Run unit tests with coverage
	$(GO) test -race -coverprofile=coverage.out ./...

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
