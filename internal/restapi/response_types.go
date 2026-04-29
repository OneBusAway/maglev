package restapi

import "maglev.onebusaway.org/internal/models"

type ListResponse[T any] struct {
	Code        int         `json:"code"`
	CurrentTime int64       `json:"currentTime"`
	Data        ListData[T] `json:"data,omitempty"`
	Text        string      `json:"text"`
	Version     int         `json:"version"`
}

type ListData[T any] struct {
	LimitExceeded bool                   `json:"limitExceeded"`
	List          []T                    `json:"list"`
	OutOfRange    bool                   `json:"outOfRange"`
	References    models.ReferencesModel `json:"references"`
	FieldErrors   map[string][]string    `json:"fieldErrors"`
}

type EntryResponse[T any] struct {
	Code        int          `json:"code"`
	CurrentTime int64        `json:"currentTime"`
	Data        EntryData[T] `json:"data,omitempty"`
	Text        string       `json:"text"`
	Version     int          `json:"version"`
}

type EntryData[T any] struct {
	Entry       T                      `json:"entry"`
	References  models.ReferencesModel `json:"references"`
	FieldErrors map[string][]string    `json:"fieldErrors,omitempty"`
}

type CoverageResponse ListResponse[models.AgencyCoverage]
type RoutesResponse ListResponse[models.Route]
type StopsResponse ListResponse[models.Stop]
type RouteIDsForAgencyResponse ListResponse[string]
type StopIDsForAgencyResponse ListResponse[string]
type AgencyEntryResponse EntryResponse[models.AgencyReference]
type ScheduleForRouteResponse EntryResponse[models.ScheduleForRouteEntry]
