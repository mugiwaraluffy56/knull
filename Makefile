# Knull developer workflow.
#
# Common entry points for the whole workspace. `make help` lists them.
# The typical first run is: `make deps` then `make up` then `make dev`.

.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help deps up down logs dev dev-api dev-web \
        build build-backend build-web \
        test test-backend fmt fmt-backend lint lint-web \
        typecheck-web gen-api check migrate

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

deps: ## Install backend and frontend dependencies
	cd backend && go mod download
	cd web && pnpm install

up: ## Start PostgreSQL and Redis via docker-compose
	docker compose up -d
	@echo "waiting for services to become healthy..."
	@until [ "$$(docker inspect -f '{{.State.Health.Status}}' $$(docker compose ps -q postgres))" = "healthy" ]; do sleep 1; done
	@until [ "$$(docker inspect -f '{{.State.Health.Status}}' $$(docker compose ps -q redis))" = "healthy" ]; do sleep 1; done
	@echo "postgres and redis are healthy"

down: ## Stop docker-compose services
	docker compose down

logs: ## Tail docker-compose logs
	docker compose logs -f

dev: ## Print how to run both dev servers
	@echo "Run these in two terminals:"
	@echo "  make dev-api   # Go incident API on :8080"
	@echo "  make dev-web   # Next.js UI on :3000"

dev-api: ## Run the Go incident API (requires 'make up')
	cd backend && go run ./cmd/api

migrate: ## Apply database migrations
	cd backend && go run ./cmd/migrate

dev-web: ## Run the Next.js UI
	cd web && pnpm dev

gen-api: ## Regenerate the TypeScript client from the OpenAPI contract
	cd web && pnpm gen:api

build: build-backend build-web ## Build backend and frontend

build-backend: ## Compile the Go backend
	cd backend && go build ./...

build-web: ## Build the Next.js app
	cd web && pnpm build

test: test-backend ## Run all tests

test-backend: ## Run Go tests
	cd backend && go test ./...

fmt: fmt-backend ## Format all code
	cd web && pnpm exec prettier --write . 2>/dev/null || true

fmt-backend: ## Format Go code
	cd backend && gofmt -w .

check: ## Run backend vet+test and frontend typecheck+lint+build
	cd backend && go vet ./... && go test ./...
	cd web && pnpm gen:api && pnpm typecheck && pnpm lint && pnpm build

typecheck-web: ## Typecheck the frontend
	cd web && pnpm typecheck

lint-web: ## Lint the frontend
	cd web && pnpm lint
