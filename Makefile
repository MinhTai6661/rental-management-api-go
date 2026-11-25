# Every Go tool runs inside Docker, so nothing has to be installed on the host.
GO_IMAGE      ?= golang:1.27.4
LINT_IMAGE    ?= golangci/golangci-lint:v2.15.0
SQLC_IMAGE    ?= sqlc/sqlc:1.30.0
AIR_VERSION   ?= v1.62.0
# 8080 is taken by the Laravel reverb container, so the dev server is published on 8090.
HTTP_PORT     ?= 8090

DOCKER_RUN = docker run --rm -v "$(CURDIR)":/src -w /src \
	-v rental-management-go-mod:/go/pkg/mod -v rental-management-go-build:/root/.cache/go-build

GO = $(DOCKER_RUN) $(GO_IMAGE) go

# The compose stack (see compose.yaml). Commands that talk to its database run in a throwaway container joined to
# its network, so they use the in-network address instead of the published port.
COMPOSE_NETWORK ?= rental_management_network
DEV_DATABASE_URL ?= postgres://rental_management:rental_management@postgres:5432/rental_management_dev?sslmode=disable
SEED_DEMO_PASSWORD ?= demo-password-123
# Development only (the same value as compose.yaml): the key that encrypts the links of bills. Never use it elsewhere.
DEV_DATA_ENCRYPTION_KEY ?= MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
RENTAL_MANAGEMENT = docker run --rm --network $(COMPOSE_NETWORK) -e DATABASE_URL="$(DEV_DATABASE_URL)" -e APP_ENV=local \
	-e SEED_DEMO_PASSWORD="$(SEED_DEMO_PASSWORD)" -e DATA_ENCRYPTION_KEY="$(DEV_DATA_ENCRYPTION_KEY)" \
	-v "$(CURDIR)":/src -w /src -v rental-management-go-mod:/go/pkg/mod -v rental-management-go-build:/root/.cache/go-build \
	$(GO_IMAGE) go run ./cmd/rental-management

# `make migrate-media` copies the pictures of the compose media volume into the dev SeaweedFS bucket (the keys are the
# development ones of docker/seaweedfs/s3.json). To fill R2 or AWS instead, set STORAGE_DRIVER/S3_* in the shell, e.g.
#   S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com S3_BUCKET=... S3_ACCESS_KEY_ID=... S3_SECRET_ACCESS_KEY=... \
#   S3_REGION=auto S3_PATH_STYLE=false COMPOSE_NETWORK=bridge make migrate-media
MEDIA_VOLUME      ?= rental-management_rental-management-media
S3_ENDPOINT       ?= http://seaweedfs:8333
S3_BUCKET         ?= rental-management-media
S3_ACCESS_KEY_ID  ?= dev-access-key
S3_SECRET_ACCESS_KEY ?= dev-secret-key-change-me
S3_REGION         ?= us-east-1
S3_PATH_STYLE     ?= true

.PHONY: help build test test-integration vet fmt fmt-check lint tidy sqlc dev check hooks up down logs migrate \
	migrate-down migrate-status seed seed-demo expire-subscriptions send-invoices send-reminders prune-notifications audit-maintain dispatch-announcements worker migrate-media fetch-locations

help: ## List the targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "%-8s %s\n", $$1, $$2}'

build: ## Build the binary into ./bin/rental-management
	$(GO) build -trimpath -ldflags="-s -w" -o bin/rental-management ./cmd/rental-management

test: ## Run the unit tests
	$(GO) test ./...

vet: ## go vet
	$(GO) vet $(VET_FLAGS) ./...

fmt: ## Format the code
	$(GO) fmt ./...

fmt-check: ## Fail when a file is not gofmt-formatted (changes nothing)
	@files="$$($(DOCKER_RUN) $(GO_IMAGE) gofmt -l .)"; \
	if [ -n "$$files" ]; then echo "not gofmt-formatted:"; echo "$$files"; exit 1; fi

lint: ## golangci-lint
	$(DOCKER_RUN) -v rental-management-lint-cache:/root/.cache $(LINT_IMAGE) golangci-lint run

tidy: ## go mod tidy
	$(GO) mod tidy

sqlc: ## Generate the sqlc code (internal/adapter/persistence/postgres/sqlc) from db/migrations and db/queries
	$(DOCKER_RUN) $(SQLC_IMAGE) generate

test-integration: ## Integration tests: throwaway Postgres/Redis containers started from inside Docker
	docker run --rm -v "$(CURDIR)":/src -w /src \
		-v rental-management-go-mod:/go/pkg/mod -v rental-management-go-build:/root/.cache/go-build \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-e TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock \
		-e TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal \
		--add-host host.docker.internal:host-gateway \
		$(GO_IMAGE) go test -tags=integration -count=1 -p 1 ./...

up: ## Start the dev stack (api, postgres, redis, mailpit)
	docker compose up -d

down: ## Stop the dev stack (the data volumes are kept)
	docker compose down

logs: ## Follow the api logs
	docker compose logs -f api

migrate: ## Apply the pending migrations to the dev database
	$(RENTAL_MANAGEMENT) migrate up

migrate-down: ## Roll back the latest migration of the dev database
	$(RENTAL_MANAGEMENT) migrate down

migrate-status: ## Show which migrations are applied
	$(RENTAL_MANAGEMENT) migrate status

seed: ## Load roles, permissions, service plans, provinces and wards into the dev database
	$(RENTAL_MANAGEMENT) seed

seed-demo: ## Also create the demo accounts (password: SEED_DEMO_PASSWORD, default for the dev stack: see compose.yaml)
	$(RENTAL_MANAGEMENT) seed -demo

expire-subscriptions: ## Mark the subscriptions past their end as expired (idempotent; the worker runs it daily)
	$(RENTAL_MANAGEMENT) subscriptions-expire

send-invoices: ## Send the bills of plans with automatic invoicing (idempotent; the worker runs it daily)
	$(RENTAL_MANAGEMENT) invoices-send-automatic

send-reminders: ## Remind people of bills about to fall due or overdue (once per bill; the worker runs it daily)
	$(RENTAL_MANAGEMENT) invoices-send-reminders

audit-maintain: ## Make the audit partitions of the coming months and drop the expired ones (idempotent; the worker runs it daily)
	$(RENTAL_MANAGEMENT) audit-maintain

prune-notifications: ## Delete the notifications past NOTIFICATION_RETENTION_DAYS (idempotent; the worker runs it daily)
	$(RENTAL_MANAGEMENT) notifications-prune

dispatch-announcements: ## Send the announcements whose start has come (idempotent; the worker runs it every minute)
	$(RENTAL_MANAGEMENT) announcements-dispatch-due

worker: ## Run the background jobs (daily commands and the announcement dispatch) in the foreground; Ctrl+C stops it
	docker compose --profile worker up worker

migrate-media: ## Copy the local pictures into the S3 bucket (idempotent, never deletes the local files)
	docker run --rm --network $(COMPOSE_NETWORK) -e APP_ENV=local -e STORAGE_DRIVER=s3 \
		-e S3_ENDPOINT -e S3_BUCKET -e S3_ACCESS_KEY_ID -e S3_SECRET_ACCESS_KEY -e S3_REGION -e S3_PATH_STYLE \
		-e MEDIA_DIR=/data/media -v $(MEDIA_VOLUME):/data/media:ro \
		-v "$(CURDIR)":/src -w /src -v rental-management-go-mod:/go/pkg/mod -v rental-management-go-build:/root/.cache/go-build \
		$(GO_IMAGE) go run ./cmd/rental-management media-upload

fetch-locations: ## Download the provinces and wards snapshot again (network; only when the divisions change)
	$(GO) run ./tools/fetchlocations

dev: ## Run with reload (air) on http://localhost:$(HTTP_PORT)
	docker run --rm -it -v "$(CURDIR)":/src -w /src -p $(HTTP_PORT):8080 \
		-v rental-management-go-mod:/go/pkg/mod -v rental-management-go-build:/root/.cache/go-build \
		-e APP_ENV=local -e LOG_FORMAT=text $(GO_IMAGE) \
		go run github.com/air-verse/air@$(AIR_VERSION) -c .air.toml

check: fmt-check vet test ## gofmt check + vet + unit tests (run `make lint` too before pushing)

hooks: ## Use the git hooks in .githooks (Conventional Commits check, pre-push checks)
	git config core.hooksPath .githooks
	chmod +x .githooks/commit-msg .githooks/pre-push
