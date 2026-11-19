package models

type Situation struct {
	ID                 string                 `json:"id"`
	CreationTime       ModelTime              `json:"creationTime"`
	ActiveWindows      []ActiveWindow         `json:"activeWindows"`
	AllAffects         []AffectedEntity       `json:"allAffects"`
	ConsequenceMessage string                 `json:"consequenceMessage"`
	Consequences       []SituationConsequence `json:"consequences"`
	PublicationWindows []any                  `json:"publicationWindows"`
	Reason             string                 `json:"reason"`
	Severity           string                 `json:"severity"`
	Summary            *TranslatedString      `json:"summary,omitempty"`
	Description        *TranslatedString      `json:"description,omitempty"`
	URL                *TranslatedString      `json:"url,omitempty"`
}

type SituationConsequence struct {
	Condition        string                     `json:"condition"`
	ConditionDetails *SituationConditionDetails `json:"conditionDetails,omitempty"`
}

type SituationConditionDetails struct {
	DiversionPath    Polyline `json:"diversionPath"`
	DiversionStopIDs []string `json:"diversionStopIds"`
}

type ActiveWindow struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type AffectedEntity struct {
	AgencyID      string `json:"agencyId"`
	ApplicationID string `json:"applicationId"`
	DirectionID   string `json:"directionId"`
	RouteID       string `json:"routeId"`
	StopID        string `json:"stopId"`
	TripID        string `json:"tripId"`
}

type TranslatedString struct {
	Value string `json:"value,omitempty"`
	Lang  string `json:"lang,omitempty"`
}
