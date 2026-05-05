.PHONY: run build test fmt lint migrate-up migrate-down migrate-create seed deploy

DB_URL ?= $(DATABASE_URL)
MIGRATIONS_PATH = db/migrations

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./...

fmt:
	gofmt -s -w .

lint:
	golangci-lint run ./...

migrate-up:
	migrate -database "$(DB_URL)" -path $(MIGRATIONS_PATH) up

migrate-down:
	migrate -database "$(DB_URL)" -path $(MIGRATIONS_PATH) down 1

migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<snake_case_name>"; exit 1)
	migrate create -ext sql -dir $(MIGRATIONS_PATH) -seq $(name)

seed:
	go run ./db/seeds

deploy:
	fly deploy
