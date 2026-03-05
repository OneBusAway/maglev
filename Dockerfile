# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGO (required by mattn/go-sqlite3)
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled (required for SQLite)
ARG GIT_COMMIT=unknown
ARG GIT_BRANCH=unknown
ARG BUILD_TIME=unknown
ARG VERSION=dev
ARG GIT_DIRTY=false
ARG GIT_EMAIL=unknown
ARG GIT_NAME=unknown
ARG GIT_REMOTE=unknown
ARG GIT_MSG=unknown
ARG BUILD_HOST=docker
ARG GIT_COMMIT_TIME=unknown

ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux GOARCH=${TARGETARCH} go build -tags sqlite_fts5 \
    -ldflags "-X 'maglev.onebusaway.org/internal/buildinfo.CommitHash=${GIT_COMMIT}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.Branch=${GIT_BRANCH}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.BuildTime=${BUILD_TIME}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.Version=${VERSION}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.CommitTime=${GIT_COMMIT_TIME}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.Dirty=${GIT_DIRTY}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.UserEmail=${GIT_EMAIL}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.UserName=${GIT_NAME}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.RemoteURL=${GIT_REMOTE}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.CommitMessage=${GIT_MSG}' \
              -X 'maglev.onebusaway.org/internal/buildinfo.Host=${BUILD_HOST}'" \
    -o maglev ./cmd/api

# Runtime stage
FROM alpine:3.21

LABEL org.opencontainers.image.source="https://github.com/OneBusAway/maglev"
LABEL org.opencontainers.image.description="REST API server for OneBusAway transit data"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# Configuration for non-root user
ARG USER_ID=1000
ARG GROUP_ID=1000

# Install runtime dependencies
# - ca-certificates: for HTTPS requests to GTFS feeds
# - tzdata: for timezone parsing support
# - wget: for health check
# - sqlite3 to support in-container database inspection
RUN apk add --no-cache ca-certificates tzdata wget sqlite

# Create non-root user for security
RUN addgroup -g ${GROUP_ID} maglev && \
    adduser -u ${USER_ID} -G maglev -s /bin/sh -D maglev

WORKDIR /app

# Create data directory for SQLite database persistence
RUN mkdir -p /app/data && chown -R maglev:maglev /app

# Copy binary from builder
COPY --from=builder /build/maglev .
# Copy example config (users should mount their own config.json)
COPY --from=builder /build/config.example.json ./config.example.json
COPY --from=builder /build/index.html ./index.html
COPY --from=builder /build/marketing/ ./marketing/

# Set ownership
RUN chown -R maglev:maglev /app

# Switch to non-root user
USER maglev

# Expose API port
EXPOSE 4000

# Health check using the current-time endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --spider "http://localhost:4000/healthz" 2>&1 || exit 1

# Default command - run with config file
# Users should mount config.json or use command-line flags
CMD ["./maglev", "-f", "config.json"]
