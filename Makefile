# Detect OS (MSYS2 sets MSYSTEM; prefer Unix syntax over Windows CMD syntax)
ifneq ($(MSYSTEM),)
    SET_ENV := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5"
else ifeq ($(OS),Windows_NT)
    SET_ENV := set CGO_ENABLED=1 & set CGO_CFLAGS=-DSQLITE_ENABLE_FTS5 &
else
    SET_ENV := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5"
endif

DOCKER_IMAGE := opentransitsoftwarefoundation/maglev

GIT_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
GIT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT_TIME := $(shell git log -1 --pretty=format:'%aI' 2>/dev/null || echo "unknown")
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_DIRTY := $(shell test -n "`git status --porcelain`" && echo "true" || echo "false")
GIT_EMAIL := $(shell git log -1 --pretty=format:'%ae' 2>/dev/null || echo "unknown")
GIT_NAME := $(shell git log -1 --pretty=format:'%an' 2>/dev/null || echo "unknown")
GIT_REMOTE := $(shell git config --get remote.origin.url 2>/dev/null || echo "unknown")
GIT_MSG := $(shell git log -1 --pretty=format:'%s' 2>/dev/null | tr -d "'\"\`" || echo "unknown")
BUILD_HOST := $(shell hostname)

LDFLAGS := -ldflags "-X 'maglev.onebusaway.org/internal/buildinfo.CommitHash=$(GIT_COMMIT)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.Branch=$(GIT_BRANCH)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.BuildTime=$(BUILD_TIME)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.Version=$(VERSION)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.CommitTime=$(GIT_COMMIT_TIME)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.Dirty=$(GIT_DIRTY)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.UserEmail=$(GIT_EMAIL)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.UserName=$(GIT_NAME)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.RemoteURL=$(GIT_REMOTE)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.CommitMessage=$(GIT_MSG)' \
                    -X 'maglev.onebusaway.org/internal/buildinfo.Host=$(BUILD_HOST)'"

.PHONY: build build-debug clean coverage-report check-jq coverage test run lint watch fmt \
        gtfstidy models check-golangci-lint \
        docker-build docker-push docker-run docker-stop docker-compose-up docker-compose-down docker-compose-dev docker-clean docker-clean-all

run: build
	bin/maglev -f config.json

build: gtfstidy
	$(SET_ENV) go build -tags "sqlite_fts5" $(LDFLAGS) -o bin/maglev ./cmd/api

build-debug: gtfstidy
	$(SET_ENV) go build -tags "sqlite_fts5" $(LDFLAGS) -gcflags "all=-N -l" -o bin/maglev ./cmd/api

gtfstidy:
	$(SET_ENV) go build -tags "sqlite_fts5" -o bin/gtfstidy github.com/patrickbr/gtfstidy

clean:
	go clean
	rm -f maglev
	rm -f coverage.out

check-jq:
	@which jq > /dev/null 2>&1 || (echo "Error: jq is not installed. Install with: apt install jq, or brew install jq" && exit 1)

coverage-report: check-jq
	$(SET_ENV) go test -tags "sqlite_fts5" ./... -cover > /tmp/go-coverage.txt 2>&1 || (cat /tmp/go-coverage.txt && exit 1)
	grep '^ok' /tmp/go-coverage.txt | awk '{print $$2, $$5}' | jq -R 'split(" ") | {pkg: .[0], coverage: .[1]}'

coverage:
	$(SET_ENV) go test -tags "sqlite_fts5" -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

check-golangci-lint:
	@which golangci-lint > /dev/null 2>&1 || (echo "Error: golangci-lint is not installed. Please install it by running: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)

lint: check-golangci-lint
	golangci-lint run --build-tags "sqlite_fts5"

fmt:
	go fmt ./...

test:
	$(SET_ENV) go test -tags "sqlite_fts5" ./...

models:
	go tool sqlc generate -f gtfsdb/sqlc.yml

watch:
	air

# Docker targets
docker-build:
	docker build \
		--build-arg 'GIT_COMMIT=$(GIT_COMMIT)' \
		--build-arg 'GIT_BRANCH=$(GIT_BRANCH)' \
		--build-arg 'BUILD_TIME=$(BUILD_TIME)' \
		--build-arg 'VERSION=$(VERSION)' \
		--build-arg 'GIT_DIRTY=$(GIT_DIRTY)' \
		--build-arg 'GIT_NAME=$(GIT_NAME)' \
		--build-arg 'GIT_EMAIL=$(GIT_EMAIL)' \
		--build-arg 'GIT_REMOTE=$(GIT_REMOTE)' \
		--build-arg 'GIT_MSG=$(GIT_MSG)' \
		--build-arg 'BUILD_HOST=$(BUILD_HOST)' \
		--build-arg 'GIT_COMMIT_TIME=$(GIT_COMMIT_TIME)' \
		-t $(DOCKER_IMAGE) .

docker-push: docker-build
	docker push $(DOCKER_IMAGE):latest

docker-run: docker-build
	docker run --name maglev -p 4000:4000 \
		-v $(PWD)/config.docker.json:/app/config.json:ro \
		-v maglev-data:/app/data $(DOCKER_IMAGE)

docker-stop:
	docker stop maglev 2>/dev/null || true
	docker rm maglev 2>/dev/null || true

docker-compose-up:
	docker-compose up -d

docker-compose-down:
	docker-compose down || echo "Note: docker-compose down failed (may not be running)"
	docker-compose -f docker-compose.dev.yml down || echo "Note: docker-compose dev down failed (may not be running)"

docker-compose-dev:
	docker-compose -f docker-compose.dev.yml up

docker-clean-all:
	@echo "WARNING: This will delete all data volumes!"
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || exit 1
	docker-compose down -v || echo "Note: docker-compose down -v failed (may not be running)"
	docker-compose -f docker-compose.dev.yml down -v || echo "Note: docker-compose dev down -v failed (may not be running)"
	@echo "Removing Docker images..."
	@if docker image inspect $(DOCKER_IMAGE):latest >/dev/null 2>&1; then docker rmi $(DOCKER_IMAGE):latest && echo "Removed $(DOCKER_IMAGE):latest" || echo "Warning: Could not remove $(DOCKER_IMAGE):latest (may be in use)"; fi
	@if docker image inspect $(DOCKER_IMAGE):dev >/dev/null 2>&1; then docker rmi $(DOCKER_IMAGE):dev && echo "Removed $(DOCKER_IMAGE):dev" || echo "Warning: Could not remove $(DOCKER_IMAGE):dev (may be in use)"; fi
	@echo "Cleanup complete."

docker-clean:
	docker-compose down || echo "Note: docker-compose down failed (may not be running)"
	docker-compose -f docker-compose.dev.yml down || echo "Note: docker-compose dev down failed (may not be running)"
	@echo "Removing Docker images..."
	@if docker image inspect $(DOCKER_IMAGE):latest >/dev/null 2>&1; then docker rmi $(DOCKER_IMAGE):latest && echo "Removed $(DOCKER_IMAGE):latest" || echo "Warning: Could not remove $(DOCKER_IMAGE):latest (may be in use)"; fi
	@if docker image inspect $(DOCKER_IMAGE):dev >/dev/null 2>&1; then docker rmi $(DOCKER_IMAGE):dev && echo "Removed $(DOCKER_IMAGE):dev" || echo "Warning: Could not remove $(DOCKER_IMAGE):dev (may be in use)"; fi
	@echo "Cleanup complete."
