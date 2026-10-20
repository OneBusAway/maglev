package restapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The parity tests pin what an import of the RABA fixture stores. A change
// that is meant to keep behavior must leave the golden files untouched; a
// change that is meant to alter it shows what changed as a diff of the golden
// files.
//
// Regenerate the golden files with:
//
//	go test -tags "sqlite_fts5 sqlite_math_functions" ./internal/restapi -run TestParity -update-parity
var updateParity = flag.Bool("update-parity", false, "rewrite the parity golden files under testdata/parity")

const parityGoldenDir = "../../testdata/parity/raba"

// parityIgnoredColumns are import timestamps, which differ between two
// imports of the same feed.
var parityIgnoredColumns = map[string]bool{
	"import_metadata.import_time": true,
	"block_trip_index.created_at": true,
}

// parityTableQueries replaces the generic dump for tables with a foreign key
// to an autoincrement ID, so the reference is compared by its natural key.
var parityTableQueries = map[string]string{
	"block_trip_entry": `SELECT bti.index_key, bte.trip_id, bte.block_id, bte.service_id, bte.block_trip_sequence
		FROM block_trip_entry bte JOIN block_trip_index bti ON bti.id = bte.block_trip_index_id`,
}

type tableDigest struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

func TestParityTables(t *testing.T) {
	db := createTestApiWithFeed(t, "../../testdata/raba.zip").GtfsManager.GtfsDB.DB

	tables := parityTables(t, db)
	digests := map[string]tableDigest{}
	for _, table := range tables {
		rows := normalizedTableRows(t, db, table)
		sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
		digests[table] = tableDigest{Rows: len(rows), SHA256: hex.EncodeToString(sum[:])}
	}

	if !assertParityGolden(t, "tables.json", digests, *updateParity) {
		dir := writeParityDumps(t, db, tables)
		t.Logf("normalized rows written to %s; run the same test on main and diff the two directories", dir)
	}
}

// parityTables lists the ordinary tables. Virtual tables and their shadow
// tables are left out: they are derived from the tables that are compared.
func parityTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	return queryStrings(t, db, `SELECT name FROM pragma_table_list
		WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
}

// normalizedTableRows returns the table's rows as sorted JSON arrays, without
// autoincrement keys and import timestamps.
func normalizedTableRows(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	query, ok := parityTableQueries[table]
	if !ok {
		query = fmt.Sprintf("SELECT %s FROM %s", strings.Join(parityColumns(t, db, table), ", "), table)
	}
	rows, err := db.Query(query)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	require.NoError(t, err)
	var result []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		encoded, err := json.Marshal(values)
		require.NoError(t, err)
		result = append(result, string(encoded))
	}
	require.NoError(t, rows.Err())
	slices.Sort(result)
	return result
}

// parityColumns returns the table's columns minus its autoincrement key and
// import timestamps. It fails for a foreign key to another table's
// autoincrement ID, whose value depends on insertion order; such a table needs
// an entry in parityTableQueries.
func parityColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	autoincrementTables := queryStrings(t, db, `SELECT name FROM sqlite_master WHERE type = 'table' AND sql LIKE '%AUTOINCREMENT%'`)

	for _, referenced := range queryStrings(t, db, `SELECT "table" FROM pragma_foreign_key_list(?) WHERE "to" = 'id'`, table) {
		require.NotContains(t, autoincrementTables, referenced,
			"%s references an autoincrement ID in %s; add it to parityTableQueries", table, referenced)
	}

	var columns []string
	for _, column := range queryStrings(t, db, `SELECT name FROM pragma_table_info(?)`, table) {
		isAutoincrementKey := column == "id" && slices.Contains(autoincrementTables, table)
		if isAutoincrementKey || parityIgnoredColumns[table+"."+column] {
			continue
		}
		columns = append(columns, column)
	}
	return columns
}

func queryStrings(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var result []string
	for rows.Next() {
		var value string
		require.NoError(t, rows.Scan(&value))
		result = append(result, value)
	}
	require.NoError(t, rows.Err())
	return result
}

// assertParityGolden compares actual with the golden file, or rewrites the
// golden file when write is set. It reports whether they matched.
func assertParityGolden(t *testing.T, name string, actual any, write bool) bool {
	t.Helper()
	encoded, err := json.MarshalIndent(actual, "", "  ")
	require.NoError(t, err)
	encoded = append(encoded, '\n')

	path := filepath.Join(parityGoldenDir, name)
	if write {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, encoded, 0o644))
		return true
	}
	expected, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden file; run with -update-parity")
	return assert.Equal(t, string(expected), string(encoded), "%s differs from the golden file; if the change is intended, run with -update-parity", name)
}

// writeParityDumps writes each table's normalized rows to a new directory
// that outlives the test, so the rows can be diffed against another run.
func writeParityDumps(t *testing.T, db *sql.DB, tables []string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "maglev-parity-")
	require.NoError(t, err)
	for _, table := range tables {
		rows := normalizedTableRows(t, db, table)
		path := filepath.Join(dir, table+".jsonl")
		require.NoError(t, os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644))
	}
	return dir
}
