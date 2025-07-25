<img src="marketing/maglev-header.png" alt="OneBusAway Maglev" width="600">

# OBA Maglev

A complete rewrite of the OneBusAway (OBA) REST API server in Golang.

## Getting Started

1. Install Go 1.24.2 or later.
2. Copy `.env.example` to `.env` and fill in the required values.
3. Run `make run` to build start the server.
4. Open your browser and navigate to `http://localhost:4000/api/where/current-time.json?key=test` to verify the server works.

## Basic Commands

All basic commands are managed by our Makefile:

`make run` - Build and run the app with a fake API key: `test`.

`make build` - Build the app.

`make clean` - Delete all build and coverage artifacts.

`make coverage` - Test and generate HTML coverage artifacts.

`make test` - Run tests.

`make schema` - Dump the DB's SQL schema to a file for sqlc.

`make watch` - Build and run the app with Air for live reloading during development (automatically rebuilds and restarts on code changes).

## Directory Structure

* `bin` contains compiled application binaries, ready for deployment to a production server.
* `cmd/api` contains application-specific code for Maglev. This will include the code for running the server, reading and writing HTTP requests, and managing authentication.
* `internal` contains various ancillary packages used by our API. It will contain the code for interacting with our database, doing data validation, sending emails and so on. Basically, any code which isn’t application-specific and can potentially be reused will live in here. Our Go code under cmd/api will import the packages in the internal directory (but never the other way around).
* `migrations` contains the SQL migration files for our database.
* `remote` contains the configuration files and setup scripts for our production server.
* `go.mod` declares our project dependencies, versions and module path.
* `Makefile` contains recipes for automating common administrative tasks — like auditing our Go code, building binaries, and executing database migrations.

## Debugging

```bash
# Install Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Build the app
make build

# Start the debugger
dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./bin/maglev
```

And then you'll be able to debug in the GoLand IDE.

## SQL

We use sqlc with SQLite to generate a data access layer for our app.
Use the command `make models` to regenerate all autogenerated files.

### Important files

* `gtfsdb/models.go` Autogenerated by sqlc
* `gtfsdb/query.sql` All of our SQL queries
* `gtfsdb/query.sql.go` All of our SQL queries turned into Go code by sqlc
* `gtfsdb/schema.sql` Our database schema
* `gtfsdb/sqlc.yml` Configuration file for sqlc
