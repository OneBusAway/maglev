package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/metrics"
)

func TestNewGTFSDBConfig_QueryMetricsRecorder(t *testing.T) {
	t.Run("leaves recorder nil when metrics are disabled", func(t *testing.T) {
		dbConfig := newGTFSDBConfig(":memory:", Config{
			Env: appconf.Test,
		})

		assert.Nil(t, dbConfig.QueryMetricsRecorder)
	})

	t.Run("wires recorder when metrics are enabled", func(t *testing.T) {
		m := metrics.New()
		dbConfig := newGTFSDBConfig(":memory:", Config{
			Env:     appconf.Test,
			Metrics: m,
		})

		assert.Same(t, m, dbConfig.QueryMetricsRecorder)
	})
}
