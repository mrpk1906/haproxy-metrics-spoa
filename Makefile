BINARY_NAME := haproxy-metrics-spoa
BIN_DIR := bin
DOCKER_IMAGE := haproxy-metrics-spoa:latest
COMPOSE_E2E := docker compose -f test/e2e/docker-compose.e2e.yml

VERSION ?= 1.0.0
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.Version=$(VERSION) \
           -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.GitCommit=$(GIT_COMMIT) \
           -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build test test-race test-e2e docker-build docker-e2e-up docker-e2e-down docker-e2e-logs fmt vet clean

all: build test

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/spoa

test:
	go test -v ./...

test-race:
	go test -race -v ./...

test-e2e:
	go test -tags=e2e -v -timeout=120s ./test/e2e/...

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-e2e-up:
	$(COMPOSE_E2E) up -d --build

docker-e2e-down:
	$(COMPOSE_E2E) down -v

docker-e2e-logs:
	$(COMPOSE_E2E) logs -f

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)
	$(COMPOSE_E2E) down -v --remove-orphans 2>/dev/null || true
