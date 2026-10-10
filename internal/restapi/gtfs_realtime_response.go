package restapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

const (
	gtfsRealtimePathPrefix        = "/api/gtfs_realtime/"
	gtfsRealtimeVersion           = "2.0"
	gtfsRealtimeBinaryContentType = "application/x-google-protobuf"
	gtfsRealtimeTextContentType   = "text/plain; charset=utf-8"
)

// newGtfsRealtimeFeed starts an export feed whose header carries the
// effective request time in epoch seconds.
func newGtfsRealtimeFeed(requestTime time.Time) *gtfsrt.FeedMessage {
	return &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String(gtfsRealtimeVersion),
			Timestamp:           proto.Uint64(uint64(requestTime.Unix())),
		},
	}
}

// marshalGtfsRealtimeFeed encodes one feed in the requested format and
// returns the matching content type.
func marshalGtfsRealtimeFeed(feed *gtfsrt.FeedMessage, format gtfsRealtimeFormat) ([]byte, string, error) {
	if format == gtfsRealtimeText {
		body, err := prototext.MarshalOptions{Multiline: true}.Marshal(feed)
		return body, gtfsRealtimeTextContentType, err
	}
	body, err := proto.Marshal(feed)
	return body, gtfsRealtimeBinaryContentType, err
}

// writeGtfsRealtimeFeed marshals the whole feed before writing so that an
// encoding failure becomes a 500 instead of a truncated 200.
func (api *RestAPI) writeGtfsRealtimeFeed(w http.ResponseWriter, r *http.Request, format gtfsRealtimeFormat, feed *gtfsrt.FeedMessage) {
	body, contentType, err := marshalGtfsRealtimeFeed(feed, format)
	if err != nil {
		api.serverErrorResponse(w, r, fmt.Errorf("marshal GTFS-RT feed: %w", err))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	// The 200 is already sent, so a failed write can only be logged.
	if _, err := w.Write(body); err != nil {
		logging.LogError(logging.ForComponent(r.Context(), "http_server"), "write GTFS-RT feed", err,
			slog.String("path", r.URL.Path))
	}
}

// GtfsRealtimeCacheMiddleware marks every GTFS-RT export response no-store.
// It wraps the whole API chain because version validation can reject a
// request before it reaches the export route.
func GtfsRealtimeCacheMiddleware(next http.Handler) http.Handler {
	noStore := CacheControlMiddleware(models.CacheDurationNone, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, gtfsRealtimePathPrefix) {
			noStore.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// exportPayloadID formats a raw realtime trip, route or stop ID for an export
// payload. Realtime IDs are raw upstream IDs, so the default leaves them as
// they are and removeAgencyIds=false qualifies them with their owner. An
// unknown owner is never guessed.
func exportPayloadID(rawID, ownerAgencyID string, removeAgencyIDs bool) string {
	if removeAgencyIDs || ownerAgencyID == "" {
		return rawID
	}
	return utils.FormCombinedID(ownerAgencyID, rawID)
}

// formatGtfsServiceTime renders a duration since the service day's start as
// GTFS HH:MM:SS, allowing hours past 24.
func formatGtfsServiceTime(sinceServiceDayStart time.Duration) string {
	seconds := int64(sinceServiceDayStart / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}
