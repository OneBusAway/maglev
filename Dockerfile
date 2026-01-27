# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGO (required by mattn/go-sqlite3)
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled (required for SQLite)
RUN CGO_ENABLED=1 GOOS=linux go build -tags sqlite_fts5 -o maglev ./cmd/api

# Runtime stage
FROM alpine:3.21

# Configuration for non-root user
ARG USER_ID=1000
ARG GROUP_ID=1000

# Install runtime dependencies
# - ca-certificates: for HTTPS requests to GTFS feeds
# - tzdata: for timezone parsing support
# - wget: for health check
RUN apk add --no-cache ca-certificates tzdata wget

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

# Set ownership
RUN chown -R maglev:maglev /app

# Switch to non-root user
USER maglev

# Expose API port
EXPOSE 4000

# Health check API key (override via docker run -e or docker-compose environment)
ENV HEALTH_CHECK_KEY=test

# Health check using the current-time endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --spider "http://localhost:4000/api/where/current-time.json?key=${HEALTH_CHECK_KEY}" 2>&1 || exit 1

# Default command - run with config file
# Users should mount config.json or use command-line flags
CMD ["./maglev", "-f", "config.json"]

