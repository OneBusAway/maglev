package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// matchReason values on services-for-location list elements (wiki §3.4).
const (
	MatchReasonAreaContainsPoint      = "areaContainsPoint"
	MatchReasonStopWithinRadius       = "stopWithinRadius"
	MatchReasonAreaNearby             = "areaNearby"
	MatchReasonAreaIntersectsViewport = "areaIntersectsViewport"
	MatchReasonStopWithinViewport     = "stopWithinViewport"
)

// OnDemandService is the /api/ondemand entry and list element (wiki §2.1).
type OnDemandService struct {
	ID          string             `json:"id"`
	AgencyID    string             `json:"agencyId"`
	RouteID     *string            `json:"routeId"`
	Name        string             `json:"name"`
	ServiceKind string             `json:"serviceKind"`
	Description *string            `json:"description"`
	URL         *string            `json:"url"`
	Rules       []AvailabilityRule `json:"rules"`
	// MatchReason is set only on services-for-location list elements.
	MatchReason string `json:"matchReason,omitempty"`
}

// AvailabilityRule is one compiled origin/destination/window/calendar rule (wiki §2.2).
type AvailabilityRule struct {
	FromIds              []string `json:"fromIds"`
	ToIds                []string `json:"toIds"`
	StartPickupTime      *string  `json:"startPickupTime"`
	EndPickupTime        *string  `json:"endPickupTime"`
	EndDropOffTime       *string  `json:"endDropOffTime"`
	CalendarIds          []string `json:"calendarIds"`
	PickupType           int      `json:"pickupType"`
	DropOffType          int      `json:"dropOffType"`
	PickupBookingRuleId  *string  `json:"pickupBookingRuleId"`
	DropOffBookingRuleId *string  `json:"dropOffBookingRuleId"`
	SafeDurationFactor   *float64 `json:"safeDurationFactor"`
	SafeDurationOffset   *float64 `json:"safeDurationOffset"`
}

// ServiceArea is a zone reference: always a bbox, geometry per geometryDetail.
type ServiceArea struct {
	ID          string  `json:"id"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	// BBox is [minLon, minLat, maxLon, maxLat] (RFC 7946 order), from the full geometry.
	BBox [4]float64 `json:"bbox"`
	// Geometry is the GeoJSON geometry object; omitted (not null) at geometryDetail=none.
	Geometry json.RawMessage `json:"geometry,omitempty"`
	// DistanceToArea is metres from the query point to the nearest boundary; 0 when
	// inside. Non-null only in services-for-location point mode.
	DistanceToArea *float64 `json:"distanceToArea"`
	// NearestPointOnBoundary is [lon, lat]; null when inside or not in point mode.
	NearestPointOnBoundary *[2]float64 `json:"nearestPointOnBoundary"`
}

// LocationGroupReference lists a group's member stops (also in references.stops).
type LocationGroupReference struct {
	ID      string   `json:"id"`
	Name    *string  `json:"name"`
	StopIds []string `json:"stopIds"`
}

// BookingRule is the GTFS booking_rules vocabulary camelCased (wiki §2.4).
type BookingRule struct {
	ID                     string  `json:"id"`
	BookingType            int     `json:"bookingType"`
	PriorNoticeDurationMin *int    `json:"priorNoticeDurationMin"`
	PriorNoticeDurationMax *int    `json:"priorNoticeDurationMax"`
	PriorNoticeLastDay     *int    `json:"priorNoticeLastDay"`
	PriorNoticeLastTime    *string `json:"priorNoticeLastTime"`
	PriorNoticeStartDay    *int    `json:"priorNoticeStartDay"`
	PriorNoticeStartTime   *string `json:"priorNoticeStartTime"`
	PriorNoticeCalendarId  *string `json:"priorNoticeCalendarId"`
	Message                *string `json:"message"`
	PickupMessage          *string `json:"pickupMessage"`
	DropOffMessage         *string `json:"dropOffMessage"`
	PhoneNumber            *string `json:"phoneNumber"`
	InfoUrl                *string `json:"infoUrl"`
	BookingUrl             *string `json:"bookingUrl"`
}

// OnDemandCalendar is GOFS's calendar shape compiled from calendar + calendar_dates.
type OnDemandCalendar struct {
	ID            string   `json:"id"`
	Days          []string `json:"days"`
	StartDate     string   `json:"startDate"`
	EndDate       string   `json:"endDate"`
	ExceptedDates []string `json:"exceptedDates"`
}

// OnDemandReferences is the /ondemand references block: the six standard keys
// plus four new sections. /where keeps using ReferencesModel, so the new keys
// never appear there.
type OnDemandReferences struct {
	ReferencesModel
	ServiceAreas   []ServiceArea            `json:"serviceAreas"`
	LocationGroups []LocationGroupReference `json:"locationGroups"`
	BookingRules   []BookingRule            `json:"bookingRules"`
	Calendars      []OnDemandCalendar       `json:"calendars"`
}

// NewEmptyOnDemandReferences returns a references block whose ten arrays are all
// present and empty.
func NewEmptyOnDemandReferences() *OnDemandReferences {
	return &OnDemandReferences{
		ReferencesModel: *NewEmptyReferences(),
		ServiceAreas:    []ServiceArea{},
		LocationGroups:  []LocationGroupReference{},
		BookingRules:    []BookingRule{},
		Calendars:       []OnDemandCalendar{},
	}
}

// FormatGTFSTimeOfDay renders nanoseconds since service-day midnight as
// "HH:MM:SS"; hours may exceed 24, as in GTFS.
func FormatGTFSTimeOfDay(nanos int64) string {
	total := int64(time.Duration(nanos) / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

// NullableString maps the empty string to nil, the wire convention for absent
// optional strings on the new object types.
func NullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
