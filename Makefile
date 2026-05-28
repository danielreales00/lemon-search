# Lemon Search — repo-level dev tasks.
#
# The local database runs entirely in Docker via the Supabase CLI — no cloud
# provisioning is needed for development. Same Postgres 15 image + extensions as
# the hosted project. See docs/operations/local-dev.md.
#
# First run:
#   make db-install     # one-time: install the Supabase CLI (Homebrew)
#   make dev-env        # one-time: create .env.local from .env.example
#   open -a Docker      # ensure the Docker daemon is running
#   make db-up          # start local Postgres on :54322 (first run pulls images)
#   make db-reset       # apply migrations + extensions
#   make db-ingest      # load the businesses JSON (needs cmd/ingest — issue #20)

SHELL    := /bin/bash
SUPABASE ?= supabase
DATA     ?= businesses-2026-05-27.json
LOCAL_DB_URL ?= postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable

.PHONY: help db-install dev-env db-up db-down db-reset db-status db-psql db-ingest

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

db-install: ## Install the Supabase CLI (Homebrew, one-time)
	brew install supabase/tap/supabase

dev-env: ## Create .env.local from .env.example if it doesn't exist
	@test -f .env.local || { cp .env.example .env.local && echo "created .env.local"; }

db-up: ## Start the local Supabase Postgres (Docker) on :54322
	$(SUPABASE) start

db-down: ## Stop the local Supabase stack
	$(SUPABASE) stop

db-reset: ## Drop + recreate the local DB and apply all migrations (+ extensions)
	$(SUPABASE) db reset

db-status: ## Show local stack status + connection details
	$(SUPABASE) status

db-psql: ## Open a psql shell against the local DB
	psql "$(LOCAL_DB_URL)"

db-ingest: ## Load the businesses JSON into the local DB (needs cmd/ingest — issue #20)
	cd api && go run ./cmd/ingest -input ../$(DATA)
