package consolidation_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/consolidation"
)

// About 5 m and 200 m north of stopA.
var (
	stopA    = consolidation.Location{Lat: 47.0, Lon: -122.0}
	stopNear = consolidation.Location{Lat: 47.000045, Lon: -122.0}
	stopFar  = consolidation.Location{Lat: 47.0018, Lon: -122.0}
)

func id(s string) consolidation.StopID {
	agencyID, stopID, _ := strings.Cut(s, "_")
	return consolidation.StopID{AgencyID: agencyID, ID: stopID}
}

func TestValidate(t *testing.T) {
	stops := map[consolidation.StopID]consolidation.Location{
		id("1_a"): stopA,
		id("2_b"): stopNear,
		id("3_c"): stopFar,
		id("1_d"): stopNear,
	}

	tests := []struct {
		name string
		file string
		want []consolidation.Warning
	}{
		{name: "clean rule", file: "1_a 2_b\n"},
		{
			name: "stop absorbed into itself",
			file: "1_a 1_a\n",
			want: []consolidation.Warning{{Kind: consolidation.SelfAbsorbed, Line: 1, Stop: id("1_a")}},
		},
		{
			name: "canonical absorbed into itself drops the rest of its line",
			file: "1_a 1_a 2_b\n",
			want: []consolidation.Warning{
				{Kind: consolidation.SelfAbsorbed, Line: 1, Stop: id("1_a")},
				{Kind: consolidation.AbsorbedCanonical, Line: 1, Stop: id("1_a"), OtherLine: 1},
			},
		},
		{
			name: "same rule repeated",
			file: "1_a 2_b\n1_a 2_b\n",
			want: []consolidation.Warning{{Kind: consolidation.DuplicateRule, Line: 2, Stop: id("2_b"), OtherLine: 1}},
		},
		{
			name: "stop claimed by two canonicals",
			file: "1_a 2_b\n1_d 2_b\n",
			want: []consolidation.Warning{{Kind: consolidation.ConflictingCanonical, Line: 2, Stop: id("2_b"), OtherLine: 1}},
		},
		{
			name: "canonical absorbed on a later line",
			file: "1_a 2_b\n1_d 1_a\n",
			want: []consolidation.Warning{{Kind: consolidation.AbsorbedCanonical, Line: 1, Stop: id("1_a"), OtherLine: 2}},
		},
		{
			name: "canonical absorbed on an earlier line",
			file: "1_a 2_b\n2_b 1_d\n",
			want: []consolidation.Warning{{Kind: consolidation.AbsorbedCanonical, Line: 2, Stop: id("2_b"), OtherLine: 1}},
		},
		{
			name: "canonical not in any feed",
			file: "9_x 2_b\n",
			want: []consolidation.Warning{{Kind: consolidation.MissingCanonical, Line: 1, Stop: id("9_x")}},
		},
		{
			name: "absorbed stop not in any feed",
			file: "1_a 9_y 2_b\n",
			want: []consolidation.Warning{{Kind: consolidation.MissingAbsorbed, Line: 1, Stop: id("9_y")}},
		},
		{
			name: "line with only a canonical stop changes nothing",
			file: "9_x\n1_a 9_x\n",
			want: []consolidation.Warning{{Kind: consolidation.MissingAbsorbed, Line: 2, Stop: id("9_x")}},
		},
		{
			name: "distance is checked against the canonical stop that wins",
			file: "3_c 2_b\n1_a 2_b\n",
			want: []consolidation.Warning{{Kind: consolidation.ConflictingCanonical, Line: 2, Stop: id("2_b"), OtherLine: 1}},
		},
		{
			name: "stops far apart",
			file: "1_a 3_c\n",
			want: []consolidation.Warning{{Kind: consolidation.FarApart, Line: 1, Stop: id("3_c"), Meters: 200}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := consolidation.Parse(strings.NewReader(tt.file))
			require.NoError(t, err)

			got := consolidation.Validate(file, stops)

			require.Len(t, got, len(tt.want), "warnings: %v", got)
			for i := range tt.want {
				assert.InDelta(t, tt.want[i].Meters, got[i].Meters, 1)
				got[i].Meters = tt.want[i].Meters
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidate_PugetSoundFileProblems(t *testing.T) {
	file, err := parseFixture(t, filepath.Join(fixtureDir, "puget_sound.txt"))
	require.NoError(t, err)

	warnings := consolidation.Validate(file, nil)

	assert.Equal(t, []consolidation.Warning{
		{Kind: consolidation.ConflictingCanonical, Line: 50, Stop: id("3_1179"), OtherLine: 48},
	}, warningsOfKind(warnings, consolidation.ConflictingCanonical))
	assert.Len(t, warningsOfKind(warnings, consolidation.DuplicateRule), 3)
	assert.Empty(t, warningsOfKind(warnings, consolidation.AbsorbedCanonical))
	assert.Empty(t, warningsOfKind(warnings, consolidation.SelfAbsorbed))
}

func TestWarning_String(t *testing.T) {
	stop := id("3_1179")
	tests := []struct {
		warning consolidation.Warning
		want    string
	}{
		{
			consolidation.Warning{Kind: consolidation.SelfAbsorbed, Line: 50, Stop: stop},
			"stop consolidation file line 50: self_absorbed: 3_1179 is absorbed into itself",
		},
		{
			consolidation.Warning{Kind: consolidation.DuplicateRule, Line: 50, Stop: stop, OtherLine: 48},
			"stop consolidation file line 50: duplicate_rule: 3_1179 is already absorbed into the same stop on line 48",
		},
		{
			consolidation.Warning{Kind: consolidation.ConflictingCanonical, Line: 50, Stop: stop, OtherLine: 48},
			"stop consolidation file line 50: conflicting_canonical: 3_1179 is also absorbed on line 48; the later line wins",
		},
		{
			consolidation.Warning{Kind: consolidation.AbsorbedCanonical, Line: 50, Stop: stop, OtherLine: 48},
			"stop consolidation file line 50: absorbed_canonical: canonical stop 3_1179 is absorbed on line 48",
		},
		{
			consolidation.Warning{Kind: consolidation.MissingCanonical, Line: 50, Stop: stop},
			"stop consolidation file line 50: missing_canonical: canonical stop 3_1179 is not in any loaded feed",
		},
		{
			consolidation.Warning{Kind: consolidation.MissingAbsorbed, Line: 50, Stop: stop},
			"stop consolidation file line 50: missing_absorbed: absorbed stop 3_1179 is not in any loaded feed",
		},
		{
			consolidation.Warning{Kind: consolidation.FarApart, Line: 50, Stop: stop, Meters: 506.4},
			"stop consolidation file line 50: far_apart: 3_1179 is 506 m from its canonical stop",
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.warning.Kind), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.warning.String())
		})
	}
}

func warningsOfKind(warnings []consolidation.Warning, kind consolidation.Kind) []consolidation.Warning {
	var matching []consolidation.Warning
	for _, w := range warnings {
		if w.Kind == kind {
			matching = append(matching, w)
		}
	}
	return matching
}
