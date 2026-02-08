# Detect OS
ifeq ($(OS),Windows_NT)
    # Windows
    SET_ENV := set CGO_ENABLED=1 & set CGO_CFLAGS=-DSQLITE_ENABLE_FTS5 &
else
    # Linux/macOS
    SET_ENV := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5"
endif

.PHONY: build build-debug clean coverage test run lint watch fmt \
	gtfstidy models check-golangci-lint \
	docker-build docker-run docker-stop docker-compose-up docker-compose-down docker-compose-dev docker-clean docker-clean-all


run: build
	bin/maglev -f config.json

build: gtfstidy
	$(SET_ENV) go build -tags "sqlite_fts5" -o bin/maglev ./cmd/api

build-debug: gtfstidy
	$(SET_ENV) go build -tags "sqlite_fts5" -gcflags "all=-N -l" -o bin/maglev ./cmd/api

gtfstidy:
	$(SET_ENV) go build -tags "sqlite_fts5" -o bin/gtfstidy github.com/patrickbr/gtfstidy

clean:
	go clean
	rm -f maglev
	rm -f coverage.out

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
	docker build -t maglev .

docker-run: docker-build
	docker run --name maglev -p 4000:4000 \
		-v $(PWD)/config.docker.json:/app/config.json:ro \
		-v maglev-data:/app/data maglev

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
	@if docker image inspect maglev:latest >/dev/null 2>&1; then docker rmi maglev:latest && echo "Removed maglev:latest" || echo "Warning: Could not remove maglev:latest (may be in use)"; fi
	@if docker image inspect maglev:dev >/dev/null 2>&1; then docker rmi maglev:dev && echo "Removed maglev:dev" || echo "Warning: Could not remove maglev:dev (may be in use)"; fi
	@echo "Cleanup complete."

docker-clean:
	docker-compose down || echo "Note: docker-compose down failed (may not be running)"
	docker-compose -f docker-compose.dev.yml down || echo "Note: docker-compose dev down failed (may not be running)"
	@echo "Removing Docker images..."
	@if docker image inspect maglev:latest >/dev/null 2>&1; then docker rmi maglev:latest && echo "Removed maglev:latest" || echo "Warning: Could not remove maglev:latest (may be in use)"; fi
	@if docker image inspect maglev:dev >/dev/null 2>&1; then docker rmi maglev:dev && echo "Removed maglev:dev" || echo "Warning: Could not remove maglev:dev (may be in use)"; fi
	@echo "Cleanup complete."
