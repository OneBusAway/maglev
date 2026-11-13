package consolidation_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/consolidation"
)

const fixtureDir = "../../testdata/stop_consolidation"

// Each fixture's .expected.tsv was written by Java OneBusAway's
// EntityReplacementStrategyFactory; see testdata/stop_consolidation/README.md.
func TestParse_MatchesJava(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join(fixtureDir, "java_parity", "*.txt"))
	require.NoError(t, err)
	fixtures = append(fixtures, filepath.Join(fixtureDir, "puget_sound.txt"))
	require.Greater(t, len(fixtures), 1)

	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			expected := readExpected(t, fixture)

			file, err := parseFixture(t, fixture)

			if strings.HasPrefix(expected, "ERROR\t") {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, sortedLines(expected), formatLikeJava(file.Replacements()))
		})
	}
}

func TestParse_PugetSoundResolvesConflictToLaterLine(t *testing.T) {
	file, err := parseFixture(t, filepath.Join(fixtureDir, "puget_sound.txt"))
	require.NoError(t, err)

	canonical := file.Replacements()[consolidation.StopID{AgencyID: "3", ID: "1179"}]

	assert.Equal(t, consolidation.StopID{AgencyID: "1", ID: "46530"}, canonical)
}

func TestParse_ErrorNamesLineAndToken(t *testing.T) {
	_, err := consolidation.Parse(strings.NewReader("1_a 1_b\n# comment\nnot-an-id 1_c\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 3")
	assert.Contains(t, err.Error(), `"not-an-id"`)
}

func TestParse_KeepsRulesInFileOrderWithLineNumbers(t *testing.T) {
	file, err := consolidation.Parse(strings.NewReader("{{{\n1_a 2_b 3_c\n\n1_d\n}}}\n"))
	require.NoError(t, err)

	assert.Equal(t, []consolidation.Rule{
		{
			Line:      2,
			Canonical: consolidation.StopID{AgencyID: "1", ID: "a"},
			Absorbed:  []consolidation.StopID{{AgencyID: "2", ID: "b"}, {AgencyID: "3", ID: "c"}},
		},
		{Line: 4, Canonical: consolidation.StopID{AgencyID: "1", ID: "d"}},
	}, file.Rules)
}

func TestStopID_String(t *testing.T) {
	assert.Equal(t, "40_LS_C37_T1", consolidation.StopID{AgencyID: "40", ID: "LS_C37_T1"}.String())
}

func parseFixture(t *testing.T, path string) (*consolidation.File, error) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	return consolidation.Parse(f)
}

func readExpected(t *testing.T, fixture string) string {
	t.Helper()
	b, err := os.ReadFile(strings.TrimSuffix(fixture, ".txt") + ".expected.tsv")
	require.NoError(t, err)
	return string(b)
}

// formatLikeJava renders replacements the way JavaParity.java prints them.
// Both sides are re-sorted in Go because Java sorts by UTF-16 code unit.
func formatLikeJava(replacements map[consolidation.StopID]consolidation.StopID) []string {
	var lines []string
	for absorbed, canonical := range replacements {
		lines = append(lines, bracket(absorbed)+"\t"+bracket(canonical))
	}
	sort.Strings(lines)
	return lines
}

func sortedLines(s string) []string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	sort.Strings(lines)
	return lines
}

func bracket(id consolidation.StopID) string {
	return "[" + id.AgencyID + "|" + id.ID + "]"
}
