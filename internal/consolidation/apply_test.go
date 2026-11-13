package consolidation_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/consolidation"
)

func TestApply(t *testing.T) {
	loaded := map[consolidation.StopID]consolidation.Location{
		id("1_a"): stopA,
		id("2_b"): stopNear,
		id("2_c"): stopNear,
		id("1_d"): stopNear,
	}

	tests := []struct {
		name string
		file string
		want map[consolidation.StopID]consolidation.Outcome
	}{
		{
			name: "stop times move to a stored canonical stop",
			file: "1_a 2_b\n",
			want: map[consolidation.StopID]consolidation.Outcome{
				id("2_b"): {Canonical: id("1_a"), Moved: true},
			},
		},
		{
			name: "absorbed stop no feed loaded is left out",
			file: "1_a 9_x 2_b\n",
			want: map[consolidation.StopID]consolidation.Outcome{
				id("2_b"): {Canonical: id("1_a"), Moved: true},
			},
		},
		{
			name: "missing canonical still removes the absorbed stop",
			file: "9_x 2_b\n",
			want: map[consolidation.StopID]consolidation.Outcome{
				id("2_b"): {Canonical: id("9_x")},
			},
		},
		{
			name: "canonical that is itself absorbed is not stored",
			file: "1_a 2_b\n1_d 1_a\n",
			want: map[consolidation.StopID]consolidation.Outcome{
				id("2_b"): {Canonical: id("1_a")},
				id("1_a"): {Canonical: id("1_d"), Moved: true},
			},
		},
		{
			name: "stop absorbed into itself is removed",
			file: "1_a 1_a\n",
			want: map[consolidation.StopID]consolidation.Outcome{
				id("1_a"): {Canonical: id("1_a")},
			},
		},
		{
			name: "later line wins",
			file: "1_a 2_b\n1_d 2_b\n",
			want: map[consolidation.StopID]consolidation.Outcome{
				id("2_b"): {Canonical: id("1_d"), Moved: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := consolidation.Parse(strings.NewReader(tt.file))
			require.NoError(t, err)

			assert.Equal(t, tt.want, file.Apply(loaded))
		})
	}
}
