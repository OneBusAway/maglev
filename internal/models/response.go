package models

import (
	"time"

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
	return NewOKResponseAt(data, c.Now())
}

// NewOKResponseAt creates a successful response stamped with a specific instant.
// Use this when the payload already embeds that same instant (e.g. current-time).
func NewOKResponseAt(data any, t time.Time) ResponseModel {
	return NewResponseAt(200, data, "OK", t)
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

// NewArrivalsAndDeparturesForLocationResponse builds the entry envelope for
// arrivals-and-departures-for-location. Nil slices are emitted as empty JSON
// arrays because the OpenAPI schema marks stopIds, arrivalsAndDepartures,
// nearbyStopIds and limitExceeded as required.
func NewArrivalsAndDeparturesForLocationResponse(
	arrivalsAndDepartures []ArrivalAndDeparture,
	references ReferencesModel,
	stopIds []string,
	nearbyStopIds []StopWithDistance,
	situationIds []string,
	limitExceeded bool,
	c clock.Clock,
) ResponseModel {
	if arrivalsAndDepartures == nil {
		arrivalsAndDepartures = []ArrivalAndDeparture{}
	}
	if stopIds == nil {
		stopIds = []string{}
	}
	if nearbyStopIds == nil {
		nearbyStopIds = []StopWithDistance{}
	}
	if situationIds == nil {
		situationIds = []string{}
	}

	entryData := map[string]any{
		"arrivalsAndDepartures": arrivalsAndDepartures,
		"limitExceeded":         limitExceeded,
		"nearbyStopIds":         nearbyStopIds,
		"situationIds":          situationIds,
		"stopIds":               stopIds,
	}
	data := map[string]any{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponse(data, c)
}

// NewEmptyArrivalsAndDeparturesForLocationResponse builds the envelope for a
// query that matched nothing, keeping the populated-case shape because the
// OpenAPI schema marks entry and references required.
func NewEmptyArrivalsAndDeparturesForLocationResponse(c clock.Clock) ResponseModel {
	return NewArrivalsAndDeparturesForLocationResponse(
		nil, *NewEmptyReferences(), nil, nil, nil, false, c)
}

// NewResponse creates a standard response using the provided clock.
func NewResponse(code int, data any, text string, c clock.Clock) ResponseModel {
	return NewResponseAt(code, data, text, c.Now())
}

// NewResponseAt creates a standard response stamped with a specific instant.
func NewResponseAt(code int, data any, text string, t time.Time) ResponseModel {
	return ResponseModel{
		Code:        code,
		CurrentTime: t.UnixMilli(),
		Data:        data,
		Text:        text,
		Version:     APIVersion,
	}
}

// ResponseCurrentTime returns the current time from the provided clock as Unix milliseconds.
func ResponseCurrentTime(c clock.Clock) int64 {
	return c.Now().UnixMilli()
}
