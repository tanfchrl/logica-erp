SHELL := /bin/bash
.DEFAULT_GOAL := help

ENV_FILE ?= .env
ifneq (,$(wildcard $(ENV_FILE)))
include $(ENV_FILE)
export
endif

GO         ?= go
GOFLAGS    ?=
LDFLAGS    ?= -s -w
COMPOSE    ?= docker compose
DEV_COMPOSE := -f deploy/docker-compose.dev.yml
PROD_COMPOSE := -f deploy/docker-compose.yml

##@ Help
.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Logica ERP — make targets\n\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development
.PHONY: up
up: ## Start dev dependencies (Postgres, Gotenberg, MinIO).
	$(COMPOSE) $(DEV_COMPOSE) up -d

.PHONY: down
down: ## Stop dev dependencies.
	$(COMPOSE) $(DEV_COMPOSE) down

.PHONY: logs
logs: ## Tail dev dependency logs.
	$(COMPOSE) $(DEV_COMPOSE) logs -f --tail=100

.PHONY: api
api: ## Run the API server locally.
	$(GO) run ./cmd/api

.PHONY: worker
worker: ## Run the background worker locally.
	$(GO) run ./cmd/worker

##@ Database
.PHONY: migrate
migrate: ## Apply all pending migrations.
	$(GO) run ./cmd/logica migrate up

.PHONY: migrate-down
migrate-down: ## Roll back the latest migration (use sparingly; forward-only policy).
	$(GO) run ./cmd/logica migrate down

.PHONY: migrate-status
migrate-status: ## Show migration status.
	$(GO) run ./cmd/logica migrate status

.PHONY: seed
seed: ## Seed admin user, demo company, Indonesian COA.
	$(GO) run ./cmd/logica seed

##@ Quality
.PHONY: build
build: ## Build all binaries into ./bin.
	mkdir -p bin
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/api     ./cmd/api
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/worker  ./cmd/worker
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/logica  ./cmd/logica

.PHONY: test
test: ## Run all Go tests.
	$(GO) test ./... -race -count=1

.PHONY: test-short
test-short: ## Run only fast unit tests (skip integration).
	$(GO) test ./... -short -count=1

.PHONY: lint
lint: ## Run golangci-lint.
	golangci-lint run ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum.
	$(GO) mod tidy

##@ Frontend
.PHONY: web-install
web-install: ## Install web dependencies.
	cd web && pnpm install

.PHONY: web-dev
web-dev: ## Run the Vite dev server.
	cd web && pnpm dev

.PHONY: web-build
web-build: ## Build the production web bundle.
	cd web && pnpm build

##@ Backup / Restore
.PHONY: backup
backup: ## Dump the database to ./backups/<timestamp>.sql.gz.
	mkdir -p backups
	$(COMPOSE) $(DEV_COMPOSE) exec -T postgres pg_dump -U logica logica | gzip > backups/$(shell date +%Y%m%dT%H%M%S).sql.gz
	@echo "Wrote backups/$$(ls -t backups | head -n1)"

.PHONY: restore
restore: ## Restore from BACKUP=path/to/dump.sql.gz.
	@test -n "$(BACKUP)" || (echo "Usage: make restore BACKUP=backups/<file>.sql.gz" && exit 1)
	gunzip -c $(BACKUP) | $(COMPOSE) $(DEV_COMPOSE) exec -T postgres psql -U logica logica
