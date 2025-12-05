.PHONY: build fmt test run-api run-worker docker-up docker-down

export GOCACHE ?= $(CURDIR)/.cache/go-build
export GOMODCACHE ?= $(CURDIR)/.cache/gomodcache

build:
	go build ./cmd/api ./cmd/worker

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*' -not -path './.cache/*' -not -path './tmp/*')

test:
	go test ./cmd/... ./internal/... ./tests/...

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v
