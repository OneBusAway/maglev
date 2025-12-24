.PHONY: build clean coverage test run lint make watch fmt

run: build
	bin/maglev -f config.json

build: gtfstidy
	go build -tags "fts5" -gcflags "all=-N -l" -o bin/maglev ./cmd/api

gtfstidy:
	go build -tags "fts5" -o bin/gtfstidy github.com/patrickbr/gtfstidy

clean:
	go clean
	rm -f maglev
	rm -f coverage.out

coverage:
	go test -tags "fts5" -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

check-golangci-lint:
	@which golangci-lint > /dev/null 2>&1 || \
	(echo "Error: golangci-lint is not installed. Please install it by running: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)

lint: check-golangci-lint
	golangci-lint run

fmt:
	go fmt ./...

test:
	go test -tags "fts5" ./...

models:
	go tool sqlc generate -f gtfsdb/sqlc.yml

watch:
	air