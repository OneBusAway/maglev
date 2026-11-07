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

// NewArrivalsAndDepartureResponse builds the entry envelope for
// arrivals-and-departures-for-stop. Nil slices are emitted as empty JSON arrays.
// This matches Java for arrivalsAndDepartures and nearbyStopIds; clients such as
// OBAKit reject "nearbyStopIds": null. Unlike Java, Maglev always emits situationIds
// as an array because iOS clients require it, as documented in the endpoint spec.
func NewArrivalsAndDepartureResponse(arrivalsAndDepartures []ArrivalAndDeparture, references ReferencesModel, nearbyStopIds []string, situationIds []string, stopId string, c clock.Clock) ResponseModel {
	entryData := map[string]any{
		"arrivalsAndDepartures": emptyIfNil(arrivalsAndDepartures),
		"nearbyStopIds":         emptyIfNil(nearbyStopIds),
		"situationIds":          emptyIfNil(situationIds),
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
	entryData := map[string]any{
		"arrivalsAndDepartures": emptyIfNil(arrivalsAndDepartures),
		"limitExceeded":         limitExceeded,
		"nearbyStopIds":         emptyIfNil(nearbyStopIds),
		"situationIds":          emptyIfNil(situationIds),
		"stopIds":               emptyIfNil(stopIds),
	}
	data := map[string]any{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponse(data, c)
}

// emptyIfNil returns an empty slice for nil, so that the field encodes as []
// and not as null. It lives here and not in internal/utils because utils
// imports this package.
func emptyIfNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
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
