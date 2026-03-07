package models

// ReferencesModel References model for related data
type ReferencesModel struct {
	Agencies   []AgencyReference `json:"agencies"`
	Routes     []Route           `json:"routes"`
	Situations []Situation       `json:"situations"`
	StopTimes  []RouteStopTime   `json:"stopTimes"`
	Stops      []Stop            `json:"stops"`
	Trips      []Trip            `json:"trips"`
}

// NewEmptyReferences creates a new empty References model with initialized empty slices
func NewEmptyReferences() ReferencesModel {
	return ReferencesModel{
		Agencies:   []AgencyReference{},
		Routes:     []Route{},
		Situations: []Situation{},
		StopTimes:  []RouteStopTime{},
		Stops:      []Stop{},
		Trips:      []Trip{},
	}
}
