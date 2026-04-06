package gtfsdb

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNullStringOrEmpty(t *testing.T) {
	assert.Equal(t, "test", NullStringOrEmpty(sql.NullString{String: "test", Valid: true}))
	assert.Equal(t, "", NullStringOrEmpty(sql.NullString{String: "test", Valid: false}))
}
