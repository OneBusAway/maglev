package models

import (
	"maglev.onebusaway.org/internal/clock"
)

// ResponseModel Base response structure that can be reused
type ResponseModel struct {
	Code        int    `json:"code"`
	CurrentTime int64  `json:"currentTime"`
	Data        any    `json:"data,omitempty"`
	Text        string `json:"text"`
	Version     int    `json:"version"`
}

// NewOKResponse creates a successful response using the provided clock.
func NewOKResponse(data any, c clock.Clock) ResponseModel {
	return NewResponse(200, data, "OK", c)
}

func NewListResponse(list any, references ReferencesModel, limitExceeded bool, c clock.Clock) ResponseModel {
	data := map[string]any{
		"limitExceeded": limitExceeded,
		"list":          list,
		"references":    references,
	}
	return NewOKResponse(data, c)
}

func NewListResponseWithRange(list any, references ReferencesModel, outOfRange bool, c clock.Clock, isLimitExceeded bool) ResponseModel {
	data := map[string]any{
		"limitExceeded": isLimitExceeded,
		"list":          list,
		"outOfRange":    outOfRange,
		"references":    references,
	}
	return NewOKResponse(data, c)
}

func NewEntryResponse(entry any, references ReferencesModel, c clock.Clock) ResponseModel {
	data := map[string]any{
		"entry":      entry,
		"references": references,
	}
	return NewOKResponse(data, c)
}

func NewArrivalsAndDepartureResponse(arrivalsAndDepartures any, references ReferencesModel, nearbyStopIds []string, situationIds []string, stopId string, c clock.Clock) ResponseModel {
	entryData := map[string]any{
		"arrivalsAndDepartures": arrivalsAndDepartures,
		"nearbyStopIds":         nearbyStopIds,
		"situationIds":          situationIds,
		"stopId":                stopId,
	}
	data := map[string]any{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponse(data, c)
}

func NewArrivalsAndDeparturesForLocationResponse(
	arrivalsAndDepartures []ArrivalAndDeparture,
	references ReferencesModel,
	nearbyStopIds []StopWithDistance,
	situationIds []string,
	stopIds []string,
	limitExceeded bool,
	c clock.Clock,
) ResponseModel {
	if nearbyStopIds == nil {
		nearbyStopIds = []StopWithDistance{}
	}
	if situationIds == nil {
		situationIds = []string{}
	}
	if stopIds == nil {
		stopIds = []string{}
	}
	entryData := map[string]interface{}{
		"arrivalsAndDepartures": arrivalsAndDepartures,
		"limitExceeded":         limitExceeded,
		"nearbyStopIds":         nearbyStopIds,
		"situationIds":          situationIds,
		"stopIds":               stopIds,
	}
	data := map[string]interface{}{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponse(data, c)
}

// NewResponse creates a standard response using the provided clock.
func NewResponse(code int, data any, text string, c clock.Clock) ResponseModel {
	return ResponseModel{
		Code:        code,
		CurrentTime: ResponseCurrentTime(c),
		Data:        data,
		Text:        text,
		Version:     2,
	}
}

// ResponseCurrentTime returns the current time from the provided clock as Unix milliseconds.
func ResponseCurrentTime(c clock.Clock) int64 {
	return c.NowUnixMilli()
}
