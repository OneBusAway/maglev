.PHONY: build clean coverage test run lint make watch fmt \
	docker-build docker-run docker-stop docker-compose-up docker-compose-down docker-compose-dev docker-clean

run: build
	bin/maglev -f config.json

build: gtfstidy
	go build -gcflags "all=-N -l" -o bin/maglev ./cmd/api

gtfstidy:
	go build -o bin/gtfstidy github.com/patrickbr/gtfstidy

clean:
	go clean
	rm -f maglev
	rm -f coverage.out

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

check-golangci-lint:
	@which golangci-lint > /dev/null 2>&1 || (echo "Error: golangci-lint is not installed. Please install it by running: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)

lint: check-golangci-lint
	golangci-lint run

fmt:
	go fmt ./...

test:
	go test ./...

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
	docker-compose down

docker-compose-dev:
	docker-compose -f docker-compose.dev.yml up

docker-clean:
	docker-compose down -v
	docker-compose -f docker-compose.dev.yml down -v
	docker rmi maglev:latest maglev:dev 2>/dev/null || true
