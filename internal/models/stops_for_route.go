package models

type Polyline struct {
	Length int    `json:"length"`
	Levels string `json:"levels"`
	Points string `json:"points"`
}

type StopGroupName struct {
	Name  string   `json:"name"`
	Names []string `json:"names"`
	Type  string   `json:"type"`
}

type StopGroup struct {
	ID        string        `json:"id"`
	Name      StopGroupName `json:"name"`
	StopIds   []string      `json:"stopIds"`
	Polylines []Polyline    `json:"polylines"`
	SubGroups []StopGroup   `json:"subGroups"`
}

type StopGrouping struct {
	Type       string      `json:"type"`
	Ordered    bool        `json:"ordered"`
	StopGroups []StopGroup `json:"stopGroups"`
}

type RouteEntry struct {
	Polylines     []Polyline     `json:"polylines"`
	RouteID       string         `json:"routeId"`
	StopGroupings []StopGrouping `json:"stopGroupings"`
	StopIds       []string       `json:"stopIds"`
}
