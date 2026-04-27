package restapi

import "maglev.onebusaway.org/internal/models"

type Response[T any] struct {
	Code        int     `json:"code"`
	CurrentTime int64   `json:"currentTime"`
	Data        Data[T] `json:"data,omitempty"`
	Text        string  `json:"text"`
	Version     int     `json:"version"`
}

type Data[T any] struct {
	LimitExceeded bool                   `json:"limitExceeded"`
	List          []T                    `json:"list"`
	OutOfRange    bool                   `json:"outOfRange"`
	References    models.ReferencesModel `json:"references"`
	FieldErrors   map[string][]string    `json:"fieldErrors"`
}

type CoverageResponse Response[models.AgencyCoverage]
type RoutesResponse Response[models.Route]
type StopsResponse Response[models.Stop]
