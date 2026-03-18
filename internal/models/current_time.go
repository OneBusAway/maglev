package models

import "time"

// CurrentTimeModel Current time specific model
type CurrentTimeModel struct {
	ReadableTime string `json:"readableTime"`
	Time         int64  `json:"time"`
}

// CurrentTimeData Combined data structure for current time endpoint
type CurrentTimeData struct {
	Entry      CurrentTimeModel `json:"entry"`
	References ReferencesModel  `json:"references"`
}

// NewCurrentTimeData creates a CurrentTimeData structure based on a provided Time
func NewCurrentTimeData(t time.Time) CurrentTimeData {
	timeMillis := t.UnixNano() / int64(time.Millisecond)

	return CurrentTimeData{
		Entry: CurrentTimeModel{
			ReadableTime: t.Format(time.RFC3339),
			Time:         timeMillis,
		},
		References: *NewEmptyReferences(),
	}
}
