.PHONY: build test lint run migrate seed up down

build:
	cd backend && go build -o ../../build/capacity-server ./cmd/capacity-server
	cd frontend && npm run build

test:
	cd backend && go test ./...

lint:
	cd backend && go vet ./...
	cd frontend && npm run lint

run:
	cd backend && go run ./cmd/capacity-server server

migrate:
	cd backend && go run ./cmd/capacity-server migrate

seed:
	cd backend && go run ./cmd/capacity-server seed

up:
	docker compose -f deploy/docker-compose.yml --env-file deploy/env/dev.env up -d

down:
	docker compose -f deploy/docker-compose.yml --env-file deploy/env/dev.env down