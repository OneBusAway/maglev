package gtfs

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRTFeedConfigAlertRecordAgencyID(t *testing.T) {
	tests := []struct {
		name       string
		feed       RTFeedConfig
		wantAgency string
		wantOK     bool
	}{
		{
			name:       "first agency-ids entry wins over the fallback",
			feed:       RTFeedConfig{AgencyIDs: []string{"40", "20"}, FallbackAlertRecordAgencyID: "30"},
			wantAgency: "40",
			wantOK:     true,
		},
		{
			name:       "fallback used when agency-ids is absent",
			feed:       RTFeedConfig{FallbackAlertRecordAgencyID: "40"},
			wantAgency: "40",
			wantOK:     true,
		},
		{
			name:       "fallback used when agency-ids is empty",
			feed:       RTFeedConfig{AgencyIDs: []string{}, FallbackAlertRecordAgencyID: "40"},
			wantAgency: "40",
			wantOK:     true,
		},
		{
			name:   "unresolved without either setting",
			feed:   RTFeedConfig{},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agencyID, ok := tt.feed.alertRecordAgencyID()
			assert.Equal(t, tt.wantAgency, agencyID)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestAlertRecordFallbackDoesNotFilterIngestion(t *testing.T) {
	config := Config{RTFeeds: []RTFeedConfig{{
		ID:                          "unscoped",
		ServiceAlertsURL:            "http://example.com/alerts",
		FallbackAlertRecordAgencyID: "40",
		Enabled:                     true,
	}}}

	assert.NotContains(t, config.feedAgencyFilters(), "unscoped")
}

func TestWarnFeedsWithoutAlertRecordAgency(t *testing.T) {
	feeds := []RTFeedConfig{
		{ID: "unresolved-alerts", ServiceAlertsURL: "http://example.com/alerts"},
		{ID: "vehicles-only", VehiclePositionsURL: "http://example.com/vehicles"},
		{ID: "fallback-alerts", ServiceAlertsURL: "http://example.com/alerts", FallbackAlertRecordAgencyID: "40"},
		{ID: "scoped-alerts", ServiceAlertsURL: "http://example.com/alerts", AgencyIDs: []string{"40"}},
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	warnFeedsWithoutAlertRecordAgency(logger, feeds)

	output := logs.String()
	assert.Equal(t, 1, bytes.Count(logs.Bytes(), []byte("level=WARN")), output)
	assert.Contains(t, output, "feed=unresolved-alerts")
	assert.Contains(t, output, "fallback-alert-record-agency-id")
}
