package models

import "encoding/json"

// ReferencesModel References model for related data
type ReferencesModel struct {
	Agencies   []AgencyReference `json:"agencies"`
	Routes     []Route           `json:"routes"`
	Situations []Situation       `json:"situations"`
	StopTimes  []RouteStopTime   `json:"stopTimes"`
	Stops      []Stop            `json:"stops"`
	Trips      []Trip            `json:"trips"`
}

// Normalize ensures that all reference slices are non-nil so they serialize as []
// rather than null in JSON responses.
func (r *ReferencesModel) Normalize() {
	if r.Agencies == nil {
		r.Agencies = []AgencyReference{}
	}
	if r.Routes == nil {
		r.Routes = []Route{}
	}
	if r.Situations == nil {
		r.Situations = []Situation{}
	}
	if r.StopTimes == nil {
		r.StopTimes = []RouteStopTime{}
	}
	if r.Stops == nil {
		r.Stops = []Stop{}
	}
	if r.Trips == nil {
		r.Trips = []Trip{}
	}
}

// MarshalJSON normalizes nil slices to empty slices before marshaling to prevent
// references lists from serializing as null.
func (r ReferencesModel) MarshalJSON() ([]byte, error) {
	type Alias ReferencesModel
	r.Normalize()
	return json.Marshal((Alias)(r))
}

func NewEmptyReferences() *ReferencesModel {
	return &ReferencesModel{
		Agencies:   []AgencyReference{},
		Routes:     []Route{},
		Stops:      []Stop{},
		Trips:      []Trip{},
		Situations: []Situation{},
		StopTimes:  []RouteStopTime{},
	}
}
