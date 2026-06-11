package models

type RouteType int

type Route struct {
	AgencyID    string `json:"agencyId"`
	Color       string `json:"color"`
	Description string `json:"description"`
	ID          string `json:"id"`
	LongName    string `json:"longName"`
	// Deprecated: NullSafeShortName is an accidental leak from the legacy Java
	// OBA server. It is preserved only for backward compatibility with the
	// Wayfinder web client. Do not use in new code; prefer ShortName and ID.
	NullSafeShortName string    `json:"nullSafeShortName"`
	ShortName         string    `json:"shortName"`
	TextColor         string    `json:"textColor"`
	Type              RouteType `json:"type"`
	URL               string    `json:"url"`
}

func NewRoute(id, agencyID, shortName, longName, description string, routeType RouteType, url, color, textColor string) Route {
	nullSafeShortName := shortName
	if nullSafeShortName == "" {
		nullSafeShortName = id
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
