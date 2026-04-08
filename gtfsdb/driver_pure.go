//go:build windows && !cgo

package gtfsdb

import _ "modernc.org/sqlite" // Pure Go SQLite for Windows without a C toolchain

const DriverName = "sqlite"
