.PHONY: run migrate migrate-down test test-race cover openapi openapi-check build vet

run:
	go run ./cmd/api

migrate:
	go run ./cmd/migrate up

# Reverts one migration.
migrate-down:
	go run ./cmd/migrate down

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

cover:
	go test -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

vet:
	gofmt -l . | (! grep .) && go vet ./...

# Rebuilds docs/api/openapi.yaml from docs/api/base.yaml and docs/api/fragments/*.yaml.
openapi:
	go run ./cmd/openapi

openapi-check:
	go run ./cmd/openapi -check

build:
	go build -o bin/bms ./cmd/api
	go build -o bin/migrate ./cmd/migrate
