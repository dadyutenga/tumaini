.PHONY: run migrate test build

run:
	go run ./cmd/api

migrate:
	go run ./cmd/migrate up

test:
	go test ./...

build:
	go build -o bin/bms ./cmd/api
	go build -o bin/migrate ./cmd/migrate
