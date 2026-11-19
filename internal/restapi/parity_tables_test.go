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

// The parity tests pin what an import of the RABA fixture stores and what a
// fixed set of API requests returns. A change that is meant to keep behavior
// must leave the golden files untouched; a change that is meant to alter it
// shows what changed as a diff of the golden files.
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

// parityColumnReplacements compares a foreign key to a rowid key by the
// referenced row's natural key, because the rowid depends on insertion order.
var parityColumnReplacements = map[string]string{
	"block_trip_entry.block_trip_index_id": `(SELECT index_key FROM block_trip_index
		WHERE block_trip_index.id = block_trip_entry.block_trip_index_id)`,
}

type tableDigest struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

func TestParityTables(t *testing.T) {
	db := createTestApiWithFeed(t, "../../testdata/raba.zip").GtfsManager.GtfsDB.DB

	schema := loadParitySchema(t, db)
	tables := parityTables(t, db)
	rowsByTable := map[string][]string{}
	digests := map[string]tableDigest{}
	for _, table := range tables {
		rows := normalizedTableRows(t, db, schema, table)
		rowsByTable[table] = rows
		sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
		digests[table] = tableDigest{Rows: len(rows), SHA256: hex.EncodeToString(sum[:])}
	}

	if !assertParityGolden(t, "tables.json", digests, *updateParity) {
		dir := writeParityDumps(t, rowsByTable)
		t.Logf("normalized rows written to %s; run the same test on main and diff the two directories", dir)
	}
}

func TestParitySchemaFindsRowidKeys(t *testing.T) {
	db := createTestApiWithFeed(t, "../../testdata/raba.zip").GtfsManager.GtfsDB.DB
	schema := loadParitySchema(t, db)

	assert.Equal(t, "id", schema.rowidKeys["block_trip_index"], "an AUTOINCREMENT key aliases the rowid")
	assert.Equal(t, "id", schema.rowidKeys["import_metadata"], "a plain INTEGER PRIMARY KEY aliases the rowid")
	assert.NotContains(t, schema.rowidKeys, "stops", "a TEXT primary key is not a rowid key")
	assert.NotContains(t, schema.rowidKeys, "stop_times", "a composite primary key is not a rowid key")
	assert.Contains(t, schema.autoincrementTables, "block_trip_index")
	assert.NotContains(t, schema.autoincrementTables, "import_metadata")
}

// paritySchema describes the keys that SQLite assigns, whose values depend on
// insertion order.
type paritySchema struct {
	// autoincrementTables have an AUTOINCREMENT id, which the digest leaves out.
	autoincrementTables []string
	// rowidKeys maps each table whose primary key aliases the rowid to that
	// key's column. That includes every AUTOINCREMENT table, and also a plain
	// INTEGER PRIMARY KEY, which SQLite fills from the rowid when an insert
	// leaves it out.
	rowidKeys map[string]string
}

func loadParitySchema(t *testing.T, db *sql.DB) paritySchema {
	t.Helper()
	rowidKeys := map[string]string{}
	for _, row := range queryStringRows(t, db, `SELECT m.name, p.name FROM sqlite_master m
		JOIN pragma_table_info(m.name) p ON p.pk = 1 AND upper(p.type) = 'INTEGER'
		WHERE m.type = 'table' AND m.sql NOT LIKE '%WITHOUT ROWID%'
		AND (SELECT COUNT(*) FROM pragma_table_info(m.name) WHERE pk > 0) = 1`) {
		table, keyColumn := row[0], row[1]
		rowidKeys[table] = keyColumn
	}
	return paritySchema{
		autoincrementTables: queryStrings(t, db, `SELECT name FROM sqlite_master
			WHERE type = 'table' AND sql LIKE '%AUTOINCREMENT%'`),
		rowidKeys: rowidKeys,
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
func normalizedTableRows(t *testing.T, db *sql.DB, schema paritySchema, table string) []string {
	t.Helper()
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(parityColumns(t, db, schema, table), ", "), table)
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

// parityColumns returns the expressions that select the table's columns,
// minus its autoincrement key and import timestamps. It fails for a foreign
// key to another table's rowid key, whose value depends on insertion
// order, unless parityColumnReplacements has an entry for that column.
func parityColumns(t *testing.T, db *sql.DB, schema paritySchema, table string) []string {
	t.Helper()
	var rowidKeyReferences []string
	for _, fk := range queryStringRows(t, db, `SELECT "from", "table", coalesce("to", '') FROM pragma_foreign_key_list(?)`, table) {
		fromColumn, parentTable, toColumn := fk[0], fk[1], fk[2]
		rowidKey, isRowidKeyTable := schema.rowidKeys[parentTable]
		// A REFERENCES clause without a column name targets the primary key,
		// and SQLite reports its "to" as NULL, which the query turns into "".
		referencesRowidKey := isRowidKeyTable && (toColumn == "" || toColumn == rowidKey)
		if referencesRowidKey {
			rowidKeyReferences = append(rowidKeyReferences, fromColumn)
		}
	}

	var columns []string
	for _, column := range queryStrings(t, db, `SELECT name FROM pragma_table_info(?)`, table) {
		key := table + "." + column
		isAutoincrementKey := column == "id" && slices.Contains(schema.autoincrementTables, table)
		if isAutoincrementKey || parityIgnoredColumns[key] {
			continue
		}
		if replacement, ok := parityColumnReplacements[key]; ok {
			columns = append(columns, replacement)
			continue
		}
		require.NotContains(t, rowidKeyReferences, column,
			"%s references a rowid key; add it to parityColumnReplacements", key)
		columns = append(columns, column)
	}
	return columns
}

// queryStringRows returns the rows of a query whose columns are all text.
func queryStringRows(t *testing.T, db *sql.DB, query string, args ...any) [][]string {
	t.Helper()
	rows, err := db.Query(query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	require.NoError(t, err)
	var result [][]string
	for rows.Next() {
		values := make([]string, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		result = append(result, values)
	}
	require.NoError(t, rows.Err())
	return result
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
func writeParityDumps(t *testing.T, rowsByTable map[string][]string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "maglev-parity-")
	require.NoError(t, err)
	for table, rows := range rowsByTable {
		path := filepath.Join(dir, table+".jsonl")
		require.NoError(t, os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644))
	}
	return dir
}
