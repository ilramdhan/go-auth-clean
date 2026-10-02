-include .env
export

DB_URL = postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)
MAILPIT_UI_PORT ?= 8025
IMAGE ?= go-auth-clean:local
GOLANGCI_LINT_VERSION ?= v2.3.0
MIGRATE_VERSION ?= v4.18.3

.PHONY: help run build test test-integration test-migrations cover lint fmt tidy swagger swagger-check tools \
	db-up db-down mail-up mail-ui up docker-build migrate-up migrate-down migrate-create promote smoke

help: ## Tampilkan daftar target
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

run: ## Jalankan API lokal
	go run ./cmd/api

build: ## Build binary ke bin/api
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/api ./cmd/api

test: ## Unit test saja (tanpa docker)
	go test -short -race -count=1 ./...

test-integration: ## Semua test termasuk integrasi (butuh docker)
	go test -race -count=1 ./...

test-migrations: ## Siklus migrasi up->down->up di postgres container baru (butuh docker)
	go test -count=1 -run TestMigrations_UpDownUp -v ./internal/platform/database/

cover: ## Test + laporan coverage per fungsi
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -n 1

lint: ## golangci-lint v2
	golangci-lint run ./...

fmt: ## gofmt + swag fmt
	gofmt -w ./cmd ./internal
	go tool swag fmt

tidy:
	go mod tidy

SWAG_INIT = go tool swag init -g cmd/api/main.go --parseInternal --outputTypes json,yaml,go

swagger: ## Generate OpenAPI (docs/swagger) dari anotasi
	go tool swag fmt
	$(SWAG_INIT) -o docs/swagger

# Di repo git: regenerate lalu `git diff`. Di luar git (mis. tarball): generate
# ke direktori sementara lalu `diff -r` dengan docs/swagger (tanpa mengubah tree).
swagger-check: ## Gagal jika docs/swagger belum di-regenerate
	@if git rev-parse --is-inside-work-tree >/dev/null 2>&1 && git ls-files --error-unmatch docs/swagger >/dev/null 2>&1; then \
		$(MAKE) --no-print-directory swagger && \
		git diff --exit-code -- docs/swagger || { echo "docs/swagger out of date: jalankan make swagger"; exit 1; }; \
	else \
		tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		$(SWAG_INIT) -o "$$tmp/swagger" >/dev/null && \
		diff -r docs/swagger "$$tmp/swagger" || { echo "docs/swagger out of date: jalankan make swagger"; exit 1; }; \
	fi

tools: ## Install tool CLI (golangci-lint, migrate)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

db-up: ## Start postgres
	docker compose up -d postgres

mail-up: ## Start mailpit (SMTP dev)
	docker compose up -d mailpit

mail-ui: mail-up ## Info URL web UI mailpit
	@echo "Mailpit UI: http://localhost:$(MAILPIT_UI_PORT)"

up: ## Start semua service dev
	docker compose up -d

db-down:
	docker compose down

docker-build: ## Build image produksi
	docker build -t $(IMAGE) .

migrate-up:
	migrate -path migrations -database "$(DB_URL)" -verbose up

migrate-down:
	migrate -path migrations -database "$(DB_URL)" -verbose down 1

smoke: ## Smoke test E2E ke server yang berjalan (BASE_URL, MAILPIT_URL)
	BASE_URL=$${BASE_URL:-http://localhost:8080} MAILPIT_URL=$${MAILPIT_URL:-http://localhost:$(MAILPIT_UI_PORT)} ./scripts/smoke.sh

promote: ## Jadikan user admin: make promote EMAIL=budi@example.com
	@test -n "$(EMAIL)" || (echo "usage: make promote EMAIL=user@example.com" && exit 1)
	go run ./cmd/promote -email "$(EMAIL)"

# make migrate-create name=create_xxx_table
migrate-create:
	migrate create -ext sql -dir migrations -seq $(name)
