# OBA Maglev

A complete rewrite of the OneBusAway (OBA) REST API server in Golang.

## Directory Structure

* `bin` contains compiled application binaries, ready for deployment to a production server.
* `cmd/api` contains application-specific code for Maglev. This will include the code for running the server, reading and writing HTTP requests, and managing authentication.
* `internal` contains various ancillary packages used by our API. It will contain the code for interacting with our database, doing data validation, sending emails and so on. Basically, any code which isn’t application-specific and can potentially be reused will live in here. Our Go code under cmd/api will import the packages in the internal directory (but never the other way around).
* `migrations` contains the SQL migration files for our database.
* `remote` contains the configuration files and setup scripts for our production server.
* `go.mod` declares our project dependencies, versions and module path.
* `Makefile` contains recipes for automating common administrative tasks — like auditing our Go code, building binaries, and executing database migrations.
