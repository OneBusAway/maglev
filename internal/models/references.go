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
