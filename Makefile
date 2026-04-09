# Detect OS (MSYS2 sets MSYSTEM; prefer Unix syntax over Windows CMD syntax)
ifneq ($(MSYSTEM),)
    SET_ENV := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5 -DSQLITE_ENABLE_MATH_FUNCTIONS"
else ifeq ($(OS),Windows_NT)
    SET_ENV := set CGO_ENABLED=1 & set CGO_CFLAGS=-DSQLITE_ENABLE_FTS5 -DSQLITE_ENABLE_MATH_FUNCTIONS &
else
    SET_ENV := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5 -DSQLITE_ENABLE_MATH_FUNCTIONS"
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

.PHONY: build build-debug clean coverage-report check-jq check-k6 coverage test run lint watch fmt \
        gtfstidy models check-golangci-lint \
        test-latency bench-sqlite-all bench-sqlite-perftest \
        docker-build docker-push docker-run docker-stop docker-compose-up docker-compose-down docker-compose-dev docker-clean docker-clean-all \
        update-openapi check-openapi \
        smoketest stresstest load-test

run: build
	bin/maglev -f config.json

build: gtfstidy
	$(SET_ENV) go build -tags "sqlite_fts5" $(LDFLAGS) -o bin/maglev ./cmd/api

build-pure: gtfstidy
	CGO_ENABLED=0 go build -tags "purego" $(LDFLAGS) -o bin/maglev ./cmd/api

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

check-k6:
	@which k6 > /dev/null 2>&1 || (echo "Error: k6 is not installed. Install with: https://grafana.com/docs/k6/latest/set-up/install-k6/" && exit 1)

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

test-latency:
	$(SET_ENV) go test -tags "sqlite_fts5" ./gtfsdb/ -run "TestQueryLatency|TestExplainQueryPlans|TestConnectionPoolTuning" -v -count=1 -timeout 300s

bench-sqlite-all:
	$(SET_ENV) go test -tags "sqlite_fts5" ./gtfsdb/ -bench=. -benchmem -benchtime=5s -run=^$

bench-sqlite-perftest:
	$(SET_ENV) go test -tags "sqlite_fts5 perftest" ./gtfsdb/ -bench=BenchmarkLargeDataset -benchmem -benchtime=10s -run=^$

test-pure:
	CGO_ENABLED=0 go test -tags "purego" ./...

models:
	go tool sqlc generate -f gtfsdb/sqlc.yml

# Fetch the latest upstream OpenAPI spec and overwrite testdata/openapi.yml.
update-openapi:
	bash scripts/update-openapi.sh

# Check whether testdata/openapi.yml matches the live upstream (skipping header).
# Exits 1 if out of date (useful for CI checks).
check-openapi:
	bash scripts/check-openapi.sh

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

define run_load_test
	@set -e; \
	printf '%s\n' \
	  '{' \
	  '  "port": 4000,' \
	  '  "env": "development",' \
	  '  "api-keys": ["test"],' \
	  '  "rate-limit": $(1),' \
	  '  "log-level": "$(2)",' \
	  '  "log-format": "json",' \
	  '  "gtfs-static-feed": {' \
	  '    "url": "testdata/raba.zip",' \
	  '    "enable-gtfs-tidy": false' \
	  '  },' \
	  '  "gtfs-rt-feeds": [],' \
	  '  "data-path": "./ci-gtfs.db"' \
	  '}' > config.ci.json; \
	$(3)./bin/maglev -f config.ci.json > maglev.log 2>&1 & \
	MAGLEV_PID=$$!; \
	trap 'kill $$MAGLEV_PID 2>/dev/null || true; rm -f config.ci.json maglev.log ci-gtfs.db' EXIT; \
	echo "Waiting for Maglev to be ready..."; \
	ready=0; \
	for i in $$(seq 1 60); do \
	  if curl -sf http://localhost:4000/healthz > /dev/null 2>&1; then \
	    echo "Server is ready after $${i} attempts."; \
	    ready=1; \
	    break; \
	  fi; \
	  echo "  Attempt $${i}/60 — not ready yet, waiting 5s..."; \
	  tail -1 maglev.log 2>/dev/null || true; \
	  sleep 5; \
	done; \
	if [ "$$ready" -ne 1 ]; then \
	  echo "ERROR: Server did not become ready in time. Dumping logs:"; \
	  cat maglev.log; \
	  exit 1; \
	fi; \
	k6 run \
	  $(4) \
	  --summary-export=$(5) \
	  loadtest/k6/scenarios.js
endef

load-test: smoketest stresstest

smoketest: build check-k6
	$(call run_load_test,100,info,,--vus 5 --duration 30s,loadtest/k6/smoke-summary.json)

stresstest: build check-k6
	$(call run_load_test,1000,warn,MAGLEV_ENABLE_PPROF=1 ,-e USE_FALLBACKS=true,loadtest/k6/stress-summary.json)
		
