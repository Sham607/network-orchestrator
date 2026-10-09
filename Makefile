# Network Orchestrator — developer entry points.
#
# `make help` lists every target. Targets that mirror CI are named the same as
# the CI jobs so "works locally" means "works in CI".

SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

.PHONY: help bootstrap check lint typecheck test test-web build smoke e2e e2e-web secrets imports openapi openapi-types web up down clean

help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

bootstrap: ## Install dependencies for API and web
	go mod download
	cd apps/web && npm ci

check: lint typecheck test test-web imports secrets openapi openapi-types ## Run everything CI runs

lint: ## Format check + go vet + web eslint
	@test -z "$$(gofmt -l apps/api tests)" || { echo "gofmt found unformatted files:"; gofmt -l apps/api tests; exit 1; }
	go vet ./apps/api/...
	go vet -tags e2e ./tests/e2e/...
	cd apps/web && npm run lint

typecheck: ## TypeScript typecheck for the web app
	cd apps/web && npm run typecheck

test: ## Go unit tests
	go test ./apps/api/...

test-web: ## Web component tests (Vitest)
	cd apps/web && npm run test

build: ## Build API binary and web bundle
	go build -o apps/api/bin/api ./apps/api/cmd/api
	cd apps/web && npm run build

imports: ## Enforce module import boundaries
	./scripts/check-imports.sh

secrets: ## Scan tracked files for secrets and TLS bypasses
	./scripts/check-secrets.sh

openapi: ## Validate the OpenAPI contract
	npx --yes @redocly/cli@1 lint openapi/openapi.yaml

openapi-types: ## Fail when the generated web API types drift from the contract
	./scripts/check-openapi-types.sh

smoke: ## End-to-end smoke test (no secrets required)
	./scripts/smoke.sh

e2e: ## E2E harness: real Postgres + mocked UniFi, no secrets required
	./scripts/e2e.sh

e2e-web: ## Playwright E2E for the dashboard against a mocked UniFi
	./scripts/e2e-web.sh

web: ## Run the SPA dev server (proxies /api to localhost:8080)
	cd apps/web && npm run dev

up: ## Start the dev stack (API + Postgres)
	docker compose up --build

down: ## Stop the dev stack
	docker compose down

clean: ## Remove build artifacts
	rm -rf apps/api/bin apps/web/dist
