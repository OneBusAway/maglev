//go:build !windows || cgo

package gtfsdb

import _ "github.com/mattn/go-sqlite3" // CGO SQLite; used on non-Windows and on Windows when CGO is enabled

const DriverName = "sqlite3"
