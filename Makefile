.PHONY: help build run test test-unit test-integration lint fmt vet mocks \
	docker-up docker-down docker-up-db docker-down-db docker-logs docker-build clean

help:
	@echo "Available targets:"
	@echo "  build             Build the app binary"
	@echo "  run               Run the app locally (go run main.go)"
	@echo "  test              Run all tests"
	@echo "  test-unit         Run unit tests (internal/usecase)"
	@echo "  test-integration  Run integration tests (requires Docker)"
	@echo "  lint              Run golangci-lint"
	@echo "  fmt               Run gofmt"
	@echo "  vet               Run go vet"
	@echo "  mocks             Regenerate mocks via mockery"
	@echo "  docker-up         Start Postgres + app via docker-compose"
	@echo "  docker-down       Stop docker-compose stack"
	@echo "  docker-up-db      Start only Postgres via docker-compose"
	@echo "  docker-down-db    Stop only Postgres via docker-compose"
	@echo "  docker-logs       Tail docker-compose logs"
	@echo "  docker-build      Build the app Docker image"
	@echo "  clean             Remove build artifacts"

build:
	go build -o bin/app .

run:
	go run main.go

test:
	go test ./...

test-unit:
	go test ./internal/usecase/...

test-integration:
	go test ./internal/repository/postgres/...

lint:
	golangci-lint run ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

mocks:
	mockery

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-up-db:
	docker-compose up -d postgres

docker-down-db:
	docker-compose stop postgres

docker-logs:
	docker-compose logs -f

docker-build:
	docker-compose build

clean:
	rm -rf bin
