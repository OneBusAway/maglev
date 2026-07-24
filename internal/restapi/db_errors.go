package restapi

import (
	"database/sql"
	"errors"
	"log/slog"
)

// warnIfRealDBError logs a warning when err is a non-nil, non-ErrNoRows DB
// error. ErrNoRows is the legitimate "row missing" case and is silent on
// purpose; everything else (timeout, connection failure, corrupt row, ...) is
// infrastructure noise an operator should see.
//
// Callers that have a graceful fallback for both cases should call this
// helper next to their fallback `return`, keeping each call site to one
// line and the function body's cognitive complexity flat.
func warnIfRealDBError(err error, op string, attrs ...any) {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return
	}
	slog.Warn(op, append(attrs, slog.String("error", err.Error()))...)
}
