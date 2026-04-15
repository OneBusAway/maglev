package models

type RouteType int

type Route struct {
	AgencyID          string    `json:"agencyId"`
	Color             string    `json:"color,omitempty"`
	Description       string    `json:"description,omitempty"`
	ID                string    `json:"id"`
	LongName          string    `json:"longName,omitempty"`
	NullSafeShortName string    `json:"-"`
	ShortName         string    `json:"shortName,omitempty"`
	TextColor         string    `json:"textColor,omitempty"`
	Type              RouteType `json:"type"`
	URL               string    `json:"url,omitempty"`
}

func NewRoute(id, agencyID, shortName, longName, description string, routeType RouteType, url, color, textColor string) Route {
	nullSafeShortName := shortName
	if nullSafeShortName == "" {
		nullSafeShortName = longName
	}

	return Route{
		AgencyID:          agencyID,
		Color:             color,
		Description:       description,
		ID:                id,
		LongName:          longName,
		NullSafeShortName: nullSafeShortName,
		ShortName:         shortName,
		TextColor:         textColor,
		Type:              routeType,
		URL:               url,
	}
}

type RouteResponse struct {
	Code        int       `json:"code"`
	CurrentTime int64     `json:"currentTime"`
	Data        RouteData `json:"data"`
	Text        string    `json:"text"`
	Version     int       `json:"version"`
}

type RouteData struct {
	LimitExceeded bool            `json:"limitExceeded"`
	List          []Route         `json:"list"`
	References    ReferencesModel `json:"references"`
}
