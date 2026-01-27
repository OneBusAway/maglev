<img src="marketing/maglev-header.png" alt="OneBusAway Maglev" width="600">

# OBA Maglev

A complete rewrite of the OneBusAway (OBA) REST API server in Golang.

## Getting Started

### Option 1: Native Go Installation

1. Install Go 1.24.2 or later.
2. Copy `config.example.json` to `config.json` and fill in the required values.
3. Run `make run` to build and start the server.
4. Open your browser and navigate to `http://localhost:4000/api/where/current-time.json?key=test` to verify the server works.

### Option 2: Docker (Recommended)

Docker provides a consistent development environment across all platforms.

**Quick Start:**

```bash
# Create docker config from template
cp config.docker.example.json config.docker.json
# Edit config.docker.json with your settings

# Build and run with Docker Compose (recommended)
# Uses config.docker.json which stores data in /app/data/ for persistence
docker-compose up

# Or build and run manually
docker build -t maglev .
docker run -p 4000:4000 -v $(pwd)/config.docker.json:/app/config.json:ro -v maglev-data:/app/data maglev

```

**Verify it works:**

```bash
curl http://localhost:4000/api/where/current-time.json?key=test

```

**Development with live reload:**

```bash
docker-compose -f docker-compose.dev.yml up

```

See the [Docker](https://www.google.com/search?q=%23docker) section below for more details.

## Configuration

Maglev supports two ways to configure the server: command-line flags or a JSON configuration file.

### Command-line Flags (Default)

Run the server with command-line flags:

```bash
./bin/maglev -port 8080 -env production -api-keys "key1,key2" -rate-limit 50

```

### JSON Configuration File

Alternatively, use a JSON configuration file with the `-f` flag:

```bash
./bin/maglev -f config.json

```

An example configuration file is provided as `config.example.json`. You can copy and modify it:

```bash
cp config.example.json config.json
# Edit config.json with your settings
./bin/maglev -f config.json

```

Example `config.json`:

```json
{
  "port": 8080,
  "env": "production",
  "api-keys": ["key1", "key2", "key3"],
  "rate-limit": 50,
  "gtfs-static-feed": {
    "url": "https://example.com/gtfs.zip",
    "auth-header-name": "Authorization",
    "auth-header-value": "Bearer token456",
    "enable-gtfs-tidy": true
  },
  "gtfs-rt-feeds": [
    {
      "trip-updates-url": "https://api.example.com/trip-updates.pb",
      "vehicle-positions-url": "https://api.example.com/vehicle-positions.pb",
      "service-alerts-url": "https://api.example.com/service-alerts.pb",
      "realtime-auth-header-name": "Authorization",
      "realtime-auth-header-value": "Bearer token123"
    }
  ],
  "data-path": "/data/gtfs.db"
}

```

**Note:** The `-f` flag is mutually exclusive with other command-line flags. If you use `-f`, all other configuration flags will be ignored. The system will error if you try to use both.

**Dump Current Configuration:**

```bash
./bin/maglev --dump-config > my-config.json
# or with other flags
./bin/maglev -port 8080 -env production --dump-config > config.json

```

**JSON Schema & IDE Integration:**

A JSON schema file is provided at `config.schema.json` for IDE autocomplete and validation. To enable IDE validation, add `$schema` to your config file:

```json
{
  "$schema": "./config.schema.json",
  "port": 4000,
  "env": "development",
  ...
}

```

### Configuration Options

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `port` | integer | 4000 | API server port |
| `env` | string | "development" | Environment (development, test, production) |
| `api-keys` | array | ["test"] | API keys for authentication |
| `rate-limit` | integer | 100 | Requests per second per API key |
| `gtfs-static-feed` | object | (Sound Transit) | Static GTFS feed configuration |
| `gtfs-rt-feeds` | array | (Sound Transit) | GTFS-RT feed configurations |
| `data-path` | string | "./gtfs.db" | Path to SQLite database |

## Basic Commands

All basic commands are managed by our Makefile:

* `make run` - Build and run the app with a fake API key: `test`.
* `make build` - Build the app.
* `make clean` - Delete all build and coverage artifacts.
* `make coverage` - Test and generate HTML coverage artifacts.
* `make test` - Run tests.
* `make models` - Generate Go code from SQL queries using sqlc.
* `make watch` - Build and run the app with Air for live reloading.

### FTS5 (SQLite) builds and tests

The server uses `github.com/mattn/go-sqlite3` and SQLite FTS5 for route search. Build and test with the FTS5 tag enabled:

```bash
CGO_ENABLED=1 go test -tags "sqlite_fts5" ./...
# or
CGO_ENABLED=1 go build -tags "sqlite_fts5" ./...

```

Ensure you have a working C toolchain when CGO is enabled.

## Directory Structure

* `bin`: Compiled application binaries.
* `cmd/api`: Application-specific code (server, HTTP handling, auth).
* `internal`: Ancillary packages (database, validation, etc.). Code here is reusable and imported by `cmd/api`.
* `migrations`: SQL migration files.
* `remote`: Production server configuration and setup scripts.
* `go.mod`: Project dependencies and module path.
* `Makefile`: Automation for building, testing, and migrations.

## Debugging

```bash
# Install Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Build the app
make build

# Start the debugger
dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./bin/maglev

```

This allows debugging in the GoLand IDE.

## SQL

We use sqlc with SQLite to generate a data access layer. Use `make models` to regenerate files.

### Important files

* `gtfsdb/models.go`: Autogenerated by sqlc.
* `gtfsdb/query.sql`: All SQL queries.
* `gtfsdb/query.sql.go`: SQL turned into Go code.
* `gtfsdb/schema.sql`: Database schema.
* `gtfsdb/sqlc.yml`: sqlc configuration.

## Docker

Docker support provides a consistent environment and simplified deployment.

### Prerequisites

* Docker 20.10 or later.
* Docker Compose v2.0 or later.

### Building the Image

```bash
# Build the production image
docker build -t maglev .
# Or use make
make docker-build

```

### Running with Docker

**Note:** Ensure you have created `config.docker.json` from the template.

**Using Docker directly:**

```bash
# Run the container (mount your config file)
docker run -p 4000:4000 -v $(pwd)/config.docker.json:/app/config.json:ro maglev

# Or use make
make docker-run

```

**Using Docker Compose (recommended for production):**

```bash
# Start the service
docker-compose up -d

# View logs
docker-compose logs -f

# Stop the service
docker-compose down

```

### Development with Docker

For development with live reload:

```bash
# Start development environment with Air live reload
docker-compose -f docker-compose.dev.yml up

# Or use make
make docker-compose-dev

```

### Docker Make Targets

| Command | Description |
| --- | --- |
| `make docker-build` | Build the Docker image |
| `make docker-run` | Build and run the container |
| `make docker-stop` | Stop and remove the container |
| `make docker-compose-up` | Start with Docker Compose |
| `make docker-compose-down` | Stop Docker Compose services |
| `make docker-compose-dev` | Start development environment |
| `make docker-clean` | Remove all Docker artifacts |

### Data Persistence

The SQLite database is persisted using Docker volumes:

* **Production**: `maglev-data` volume mounted at `/app/data`.
* **Development**: `maglev-dev-data` volume.

The GTFS database is stored in `/app/data/gtfs.db` within the container.

#### Copy database to host for inspection

```bash
# Note: 'maglev' is the default container name when using docker-compose
docker cp maglev:/app/data/gtfs.db ./gtfs-backup.db

```

Once copied, you can inspect it with any SQLite client:

```bash
sqlite3 gtfs-backup.db "SELECT name FROM sqlite_master WHERE type='table';"

```

#### Check database file exists and size

```bash
docker-compose exec maglev ls -lh /app/data/

```

#### Interactive SQLite session inside container

```bash
docker-compose exec maglev sqlite3 /app/data/gtfs.db

```

**SQLite CLI commands:**

```text
.tables
.schema stops
.quit

```

**SQL queries:**

```sql
-- Count records in a table
SELECT COUNT(*) FROM stops;

-- View sample data
SELECT * FROM stops LIMIT 5;

```

#### Additional troubleshooting commands

Verify database integrity:

```bash
docker-compose exec maglev sqlite3 /app/data/gtfs.db "PRAGMA integrity_check;"

```

Check database size:

```bash
docker-compose exec maglev du -h /app/data/gtfs.db

```

View recent database modifications:

```bash
docker-compose exec maglev stat /app/data/gtfs.db

```

### Health Checks

The container includes a health check that verifies the API is responding:

```bash
# Check container health status
docker inspect --format='{{.State.Health.Status}}' maglev

```

**Important:** The health checks use the `HEALTH_CHECK_KEY` environment variable (defaults to `test`). If you change your API keys in the configuration, update this environment variable to match:

```yaml
# In docker-compose.yml or docker-compose.dev.yml
environment:
  - HEALTH_CHECK_KEY=your-api-key

```

### Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `TZ` | Timezone for the container | `UTC` |
| `HEALTH_CHECK_KEY` | API key used for health check endpoint | `test` |

### Troubleshooting

**Container fails to start:**

```bash
# Check logs
docker-compose logs maglev

# Verify config file exists
ls -la config.docker.json

```

**Health check failing:**

```bash
# Test the endpoint manually
curl http://localhost:4000/api/where/current-time.json?key=test

# If you changed api-keys, make sure HEALTH_CHECK_KEY matches
docker-compose exec maglev printenv HEALTH_CHECK_KEY

```

**Permission issues:**

* The container runs as non-root user (maglev:1000).
* Ensure mounted volumes are accessible.