# Network Orchestrator — developer entry points.
#
# `make help` lists every target. Targets that mirror CI are named the same as
# the CI jobs so "works locally" means "works in CI".

SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

DATABASE_URL ?= postgres://orchestrator:orchestrator@localhost:5432/orchestrator?sslmode=disable

.PHONY: help bootstrap check verify lint typecheck test test-web build smoke e2e e2e-web migrate migrate-down migrate-status secrets imports openapi openapi-types web up down clean

help: ## Show available targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

bootstrap: ## Install dependencies for API and web
	go mod download
	cd apps/web && npm ci --include=dev

check: lint typecheck test test-web imports secrets openapi openapi-types ## Fast local checks (lint, test, contracts, boundaries; not full CI parity)

verify: lint typecheck test test-web build imports secrets openapi openapi-types smoke ## Full local CI parity (all jobs that run without database)

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
	cd apps/web && NODE_ENV=test npm run test

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

migrate: ## Apply pending database migrations
	go run github.com/pressly/goose/v3/cmd/goose@v3.22.1 -dir ./migrations postgres "$(DATABASE_URL)" up

migrate-down: ## Roll back the most recent database migration (dev reset only)
	go run github.com/pressly/goose/v3/cmd/goose@v3.22.1 -dir ./migrations postgres "$(DATABASE_URL)" down

migrate-status: ## Show database migration status
	go run github.com/pressly/goose/v3/cmd/goose@v3.22.1 -dir ./migrations postgres "$(DATABASE_URL)" status

web: ## Run the SPA dev server (proxies /api to localhost:8080)
	cd apps/web && npm run dev

up: ## Start the dev stack (API + Postgres)
	docker compose up --build

down: ## Stop the dev stack
	docker compose down

clean: ## Remove build artifacts
	rm -rf apps/api/bin apps/web/dist
