.PHONY: run build test fmt lint migrate-up migrate-down migrate-create seed deploy legal-sync legal-check

DB_URL ?= $(DATABASE_URL)
MIGRATIONS_PATH = db/migrations
LEGAL_SRC = ../docs/legal
LEGAL_EMBED = internal/legal/tos

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

# legal-sync copies the canonical ToS markdown from docs/legal/ into the
# server tree where go:embed can pick it up. Run after editing docs/legal/.
legal-sync:
	@mkdir -p $(LEGAL_EMBED)
	cp $(LEGAL_SRC)/*.md $(LEGAL_EMBED)/

# legal-check fails if the embedded copy has drifted from docs/legal/. Run
# in CI so a stale embed never ships.
legal-check:
	@for f in $(LEGAL_SRC)/*.md; do \
	  diff -q $$f $(LEGAL_EMBED)/$$(basename $$f) || \
	    (echo "drift between $$f and embedded copy — run 'make legal-sync'"; exit 1); \
	done
