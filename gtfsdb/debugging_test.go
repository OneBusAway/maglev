package gtfsdb

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableCounts(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	client := &Client{DB: db}

	_, err = db.Exec(`
		CREATE TABLE agencies (id TEXT);
		INSERT INTO agencies VALUES ('1');
		
		CREATE TABLE stops (id TEXT);
		INSERT INTO stops VALUES ('s1'), ('s2');

		-- Create a table NOT in the whitelist to ensure it's ignored
		CREATE TABLE secret_table (id TEXT);
	`)
	require.NoError(t, err)

	counts, err := client.TableCounts()
	require.NoError(t, err)

	assert.Equal(t, 1, counts["agencies"], "Should count agencies correctly")
	assert.Equal(t, 2, counts["stops"], "Should count stops correctly")

	_, exists := counts["secret_table"]
	assert.False(t, exists, "Should not include tables outside the whitelist")
}
