.PHONY: build fmt test run-api run-worker docker-up docker-down

export GOCACHE ?= $(CURDIR)/tmp/go-build
export GOMODCACHE ?= $(CURDIR)/tmp/gomodcache

build:
	go build ./cmd/api ./cmd/worker

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

test:
	go test ./...

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v
