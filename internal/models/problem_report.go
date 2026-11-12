package models

import (
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

// ProblemReportTrip represents a trip problem report in API responses.
type ProblemReportTrip struct {
	ID                   int64   `json:"id"`
	TripID               string  `json:"tripId"`
	ServiceDate          string  `json:"serviceDate,omitempty"`
	VehicleID            string  `json:"vehicleId,omitempty"`
	StopID               string  `json:"stopId,omitempty"`
	Code                 string  `json:"code,omitempty"`
	UserComment          string  `json:"userComment,omitempty"`
	UserLat              float64 `json:"userLat,omitempty"`
	UserLon              float64 `json:"userLon,omitempty"`
	UserLocationAccuracy float64 `json:"userLocationAccuracy,omitempty"`
	UserOnVehicle        bool    `json:"userOnVehicle"`
	UserVehicleNumber    string  `json:"userVehicleNumber,omitempty"`
	CreatedAt            int64   `json:"createdAt"`
	SubmittedAt          int64   `json:"submittedAt"`
}

// NewProblemReportTrip converts a database ProblemReportsTrip to an API response model.
// tripID is the combined trip ID to return; the stored trip ID is bare.
func NewProblemReportTrip(report gtfsdb.ProblemReportsTrip, tripID string) ProblemReportTrip {
	return ProblemReportTrip{
		ID:                   report.ID,
		TripID:               tripID,
		ServiceDate:          nulls.StringOrEmpty(report.ServiceDate),
		VehicleID:            nulls.StringOrEmpty(report.VehicleID),
		StopID:               nulls.StringOrEmpty(report.StopID),
		Code:                 nulls.StringOrEmpty(report.Code),
		UserComment:          nulls.StringOrEmpty(report.UserComment),
		UserLat:              report.UserLat.Float64,
		UserLon:              report.UserLon.Float64,
		UserLocationAccuracy: report.UserLocationAccuracy.Float64,
		UserOnVehicle:        report.UserOnVehicle.Int64 == 1,
		UserVehicleNumber:    nulls.StringOrEmpty(report.UserVehicleNumber),
		CreatedAt:            report.CreatedAt,
		SubmittedAt:          report.SubmittedAt,
	}
}

// ProblemReportStop represents a stop problem report in API responses.
type ProblemReportStop struct {
	ID                   int64   `json:"id"`
	StopID               string  `json:"stopId"`
	Code                 string  `json:"code,omitempty"`
	UserComment          string  `json:"userComment,omitempty"`
	UserLat              float64 `json:"userLat,omitempty"`
	UserLon              float64 `json:"userLon,omitempty"`
	UserLocationAccuracy float64 `json:"userLocationAccuracy,omitempty"`
	CreatedAt            int64   `json:"createdAt"`
	SubmittedAt          int64   `json:"submittedAt"`
}

// NewProblemReportStop converts a database ProblemReportsStop to an API response model.
// stopID is the combined stop ID to return; the stored stop ID is bare.
func NewProblemReportStop(report gtfsdb.ProblemReportsStop, stopID string) ProblemReportStop {
	return ProblemReportStop{
		ID:                   report.ID,
		StopID:               stopID,
		Code:                 nulls.StringOrEmpty(report.Code),
		UserComment:          nulls.StringOrEmpty(report.UserComment),
		UserLat:              report.UserLat.Float64,
		UserLon:              report.UserLon.Float64,
		UserLocationAccuracy: report.UserLocationAccuracy.Float64,
		CreatedAt:            report.CreatedAt,
		SubmittedAt:          report.SubmittedAt,
	}
}
