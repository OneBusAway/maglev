package models

import "time"

// CurrentTimeModel Current time specific model
type CurrentTimeModel struct {
	ReadableTime string    `json:"readableTime"`
	Time         ModelTime `json:"time"`
}

// CurrentTimeData Combined data structure for current time endpoint
type CurrentTimeData struct {
	Entry      CurrentTimeModel `json:"entry"`
	References ReferencesModel  `json:"references"`
}

// NewCurrentTimeData creates a CurrentTimeData structure based on a provided Time,
// formatted in UTC. Use NewCurrentTimeDataInLocation to format in a specific timezone.
func NewCurrentTimeData(t time.Time) CurrentTimeData {
	return NewCurrentTimeDataInLocation(t, time.UTC)
}

// NewCurrentTimeDataInLocation creates a CurrentTimeData formatted in the given location.
// Pass the agency's IANA timezone (e.g. time.LoadLocation("America/Los_Angeles")) so that
// readableTime reflects the agency's local time with the correct UTC offset, matching
// the behavior of the Java OBA reference server.
func NewCurrentTimeDataInLocation(t time.Time, loc *time.Location) CurrentTimeData {
	return CurrentTimeData{
		Entry: CurrentTimeModel{
			ReadableTime: t.In(loc).Format(time.RFC3339),
			Time:         NewModelTime(t),
		},
		References: *NewEmptyReferences(),
	}
}
