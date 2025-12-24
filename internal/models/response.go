package models

import (
	"time"

	"maglev.onebusaway.org/internal/clock"
)

// ResponseModel Base response structure that can be reused
type ResponseModel struct {
	Code        int         `json:"code"`
	CurrentTime int64       `json:"currentTime"`
	Data        interface{} `json:"data,omitempty"`
	Text        string      `json:"text"`
	Version     int         `json:"version"`
}

// NewOKResponseWithClock creates a successful response using the provided clock.
func NewOKResponseWithClock(data interface{}, c clock.Clock) ResponseModel {
	return NewResponseWithClock(200, data, "OK", c)
}

// NewOKResponse is a helper function that returns a successful response.
// Deprecated: Use NewOKResponseWithClock for testable code.
func NewOKResponse(data interface{}) ResponseModel {
	return NewResponse(200, data, "OK")
}

func NewListResponseWithClock(list interface{}, references ReferencesModel, c clock.Clock) ResponseModel {
	data := map[string]interface{}{
		"limitExceeded": false,
		"list":          list,
		"references":    references,
	}
	return NewOKResponseWithClock(data, c)
}

func NewListResponse(list interface{}, references ReferencesModel) ResponseModel {
	data := map[string]interface{}{
		"limitExceeded": false,
		"list":          list,
		"references":    references,
	}
	return NewOKResponse(data)
}

func NewListResponseWithRangeAndClock(list interface{}, references ReferencesModel, outOfRange bool, c clock.Clock) ResponseModel {
	data := map[string]interface{}{
		"limitExceeded": false,
		"list":          list,
		"outOfRange":    outOfRange,
		"references":    references,
	}
	return NewOKResponseWithClock(data, c)
}

func NewListResponseWithRange(list interface{}, references ReferencesModel, outOfRange bool) ResponseModel {
	data := map[string]interface{}{
		"limitExceeded": false,
		"list":          list,
		"outOfRange":    outOfRange,
		"references":    references,
	}
	return NewOKResponse(data)
}

func NewEntryResponseWithClock(entry interface{}, references ReferencesModel, c clock.Clock) ResponseModel {
	data := map[string]interface{}{
		"entry":      entry,
		"references": references,
	}
	return NewOKResponseWithClock(data, c)
}

func NewEntryResponse(entry interface{}, references ReferencesModel) ResponseModel {
	data := map[string]interface{}{
		"entry":      entry,
		"references": references,
	}
	return NewOKResponse(data)
}

func NewArrivalsAndDepartureResponseWithClock(arrivalsAndDepartures interface{}, references ReferencesModel, nearbyStopIds []string, situationIds []string, stopId string, c clock.Clock) ResponseModel {
	entryData := map[string]interface{}{
		"arrivalsAndDepartures": arrivalsAndDepartures,
		"nearbyStopIds":         nearbyStopIds,
		"situationIds":          situationIds,
		"stopId":                stopId,
	}
	data := map[string]interface{}{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponseWithClock(data, c)
}

func NewArrivalsAndDepartureResponse(arrivalsAndDepartures interface{}, references ReferencesModel, nearbyStopIds []string, situationIds []string, stopId string) ResponseModel {
	entryData := map[string]interface{}{
		"arrivalsAndDepartures": arrivalsAndDepartures,
		"nearbyStopIds":         nearbyStopIds,
		"situationIds":          situationIds,
		"stopId":                stopId,
	}
	data := map[string]interface{}{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponse(data)
}

// NewResponseWithClock creates a standard response using the provided clock.
func NewResponseWithClock(code int, data interface{}, text string, c clock.Clock) ResponseModel {
	return ResponseModel{
		Code:        code,
		CurrentTime: ResponseCurrentTimeWithClock(c),
		Data:        data,
		Text:        text,
		Version:     2,
	}
}

// NewResponse Helper function to create a standard response
// Deprecated: Use NewResponseWithClock for testable code.
func NewResponse(code int, data interface{}, text string) ResponseModel {
	return ResponseModel{
		Code:        code,
		CurrentTime: ResponseCurrentTime(),
		Data:        data,
		Text:        text,
		Version:     2,
	}
}

// ResponseCurrentTimeWithClock returns the current time from the provided clock as Unix milliseconds.
func ResponseCurrentTimeWithClock(c clock.Clock) int64 {
	return c.NowUnixMilli()
}

// ResponseCurrentTime returns the current system time as Unix milliseconds.
// Deprecated: Use ResponseCurrentTimeWithClock for testable code.
func ResponseCurrentTime() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}
