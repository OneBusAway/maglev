package models

import (
	"maglev.onebusaway.org/gtfsdb"
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
func NewProblemReportTrip(report gtfsdb.ProblemReportsTrip) ProblemReportTrip {
	return ProblemReportTrip{
		ID:                   report.ID,
		TripID:               report.TripID,
		ServiceDate:          report.ServiceDate.String,
		VehicleID:            report.VehicleID.String,
		StopID:               report.StopID.String,
		Code:                 report.Code.String,
		UserComment:          report.UserComment.String,
		UserLat:              report.UserLat.Float64,
		UserLon:              report.UserLon.Float64,
		UserLocationAccuracy: report.UserLocationAccuracy.Float64,
		UserOnVehicle:        report.UserOnVehicle.Int64 == 1,
		UserVehicleNumber:    report.UserVehicleNumber.String,
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
func NewProblemReportStop(report gtfsdb.ProblemReportsStop) ProblemReportStop {
	return ProblemReportStop{
		ID:                   report.ID,
		StopID:               report.StopID,
		Code:                 report.Code.String,
		UserComment:          report.UserComment.String,
		UserLat:              report.UserLat.Float64,
		UserLon:              report.UserLon.Float64,
		UserLocationAccuracy: report.UserLocationAccuracy.Float64,
		CreatedAt:            report.CreatedAt,
		SubmittedAt:          report.SubmittedAt,
	}
}

// ProblemReportItem is a unified wrapper used by the general /api/where/problem-reports
// endpoint to return both trip and stop reports in a single list.
type ProblemReportItem struct {
	ReportType string             `json:"reportType"` // "trip" or "stop"
	TripReport *ProblemReportTrip `json:"tripReport,omitempty"`
	StopReport *ProblemReportStop `json:"stopReport,omitempty"`
}
