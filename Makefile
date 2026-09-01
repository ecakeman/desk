SHELL := /bin/bash

APP := bin/desk
GO_PACKAGES := ./cmd/... ./internal/... ./plugins/...
COMPOSE := docker compose -f deployments/compose.yml
DATABASE_URL ?= postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable
PLAYWRIGHT_IMAGE ?= mcr.microsoft.com/playwright:v1.62.1-noble

.PHONY: fmt fmt-check vet test test-integration plugins web web-lint web-test web-e2e \
	go-build build serve chat db-up db-migrate db-down

fmt:
	go fmt $(GO_PACKAGES)

fmt-check:
	@files="$$(git ls-files '*.go')"; \
	test -n "$$files"; \
	unformatted="$$(gofmt -l $$files)"; \
	test -z "$$unformatted" || { printf 'unformatted Go files:\n%s\n' "$$unformatted"; exit 1; }

vet:
	go vet $(GO_PACKAGES)

test:
	go test -p 1 $(GO_PACKAGES)

test-integration: db-up db-migrate
	DESK_DATABASE_URL="$(DATABASE_URL)" \
	DESK_MIGRATION_DIR="$(CURDIR)/migrations" \
	go test -p 1 -count=1 $(GO_PACKAGES)

plugins:
	go build -o plugins/fs/fs ./plugins/fs
	go build -o plugins/search/search ./plugins/search

web:
	npm --prefix web ci
	npm --prefix web run build

web-lint:
	npm --prefix web ci
	npm --prefix web run lint

web-test:
	npm --prefix web ci
	npm --prefix web run test

web-e2e:
	docker run --rm --ipc=host --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
		-v "$(CURDIR)/web:/work" -w /work \
		$(PLAYWRIGHT_IMAGE) npm run e2e

go-build:
	mkdir -p bin
	go build -o $(APP) ./cmd/desk

build: plugins web go-build

serve: build
	./$(APP) serve

chat: go-build
	./$(APP) chat $(SESSION_ID)

db-up:
	$(COMPOSE) up -d --build --wait

db-migrate:
	@for file in migrations/*.sql; do \
		echo "applying $$file"; \
		$(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U desk -d desk < "$$file"; \
	done

db-down:
	$(COMPOSE) down
