package gtfsdb

import (
	"context"
	"database/sql"
)

// ProblemReportsTrip represents a problem report for a trip.
type ProblemReportsTrip struct {
	ID                   int64
	TripID               string
	ServiceDate          sql.NullString
	VehicleID            sql.NullString
	StopID               sql.NullString
	Code                 sql.NullString
	UserComment          sql.NullString
	UserLat              sql.NullFloat64
	UserLon              sql.NullFloat64
	UserLocationAccuracy sql.NullFloat64
	UserOnVehicle        sql.NullInt64
	UserVehicleNumber    sql.NullString
	CreatedAt            int64
	SubmittedAt          int64
}

// ProblemReportsStop represents a problem report for a stop.
type ProblemReportsStop struct {
	ID                   int64
	StopID               string
	Code                 sql.NullString
	UserComment          sql.NullString
	UserLat              sql.NullFloat64
	UserLon              sql.NullFloat64
	UserLocationAccuracy sql.NullFloat64
	CreatedAt            int64
	SubmittedAt          int64
}

// CreateProblemReportTripParams contains the parameters for creating a trip problem report.
type CreateProblemReportTripParams struct {
	TripID               string
	ServiceDate          sql.NullString
	VehicleID            sql.NullString
	StopID               sql.NullString
	Code                 sql.NullString
	UserComment          sql.NullString
	UserLat              sql.NullFloat64
	UserLon              sql.NullFloat64
	UserLocationAccuracy sql.NullFloat64
	UserOnVehicle        sql.NullInt64
	UserVehicleNumber    sql.NullString
	CreatedAt            int64
	SubmittedAt          int64
}

// CreateProblemReportStopParams contains the parameters for creating a stop problem report.
type CreateProblemReportStopParams struct {
	StopID               string
	Code                 sql.NullString
	UserComment          sql.NullString
	UserLat              sql.NullFloat64
	UserLon              sql.NullFloat64
	UserLocationAccuracy sql.NullFloat64
	CreatedAt            int64
	SubmittedAt          int64
}

const createProblemReportTrip = `
INSERT INTO problem_reports_trip (
    trip_id, service_date, vehicle_id, stop_id, code, user_comment,
    user_lat, user_lon, user_location_accuracy, user_on_vehicle,
    user_vehicle_number, created_at, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, trip_id, service_date, vehicle_id, stop_id, code, user_comment,
    user_lat, user_lon, user_location_accuracy, user_on_vehicle,
    user_vehicle_number, created_at, submitted_at
`

// CreateProblemReportTrip inserts a new trip problem report into the database.
func (q *Queries) CreateProblemReportTrip(ctx context.Context, arg CreateProblemReportTripParams) (ProblemReportsTrip, error) {
	row := q.db.QueryRowContext(ctx, createProblemReportTrip,
		arg.TripID,
		arg.ServiceDate,
		arg.VehicleID,
		arg.StopID,
		arg.Code,
		arg.UserComment,
		arg.UserLat,
		arg.UserLon,
		arg.UserLocationAccuracy,
		arg.UserOnVehicle,
		arg.UserVehicleNumber,
		arg.CreatedAt,
		arg.SubmittedAt,
	)
	var i ProblemReportsTrip
	err := row.Scan(
		&i.ID,
		&i.TripID,
		&i.ServiceDate,
		&i.VehicleID,
		&i.StopID,
		&i.Code,
		&i.UserComment,
		&i.UserLat,
		&i.UserLon,
		&i.UserLocationAccuracy,
		&i.UserOnVehicle,
		&i.UserVehicleNumber,
		&i.CreatedAt,
		&i.SubmittedAt,
	)
	return i, err
}

const createProblemReportStop = `
INSERT INTO problem_reports_stop (
    stop_id, code, user_comment, user_lat, user_lon,
    user_location_accuracy, created_at, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, stop_id, code, user_comment, user_lat, user_lon,
    user_location_accuracy, created_at, submitted_at
`

// CreateProblemReportStop inserts a new stop problem report into the database.
func (q *Queries) CreateProblemReportStop(ctx context.Context, arg CreateProblemReportStopParams) (ProblemReportsStop, error) {
	row := q.db.QueryRowContext(ctx, createProblemReportStop,
		arg.StopID,
		arg.Code,
		arg.UserComment,
		arg.UserLat,
		arg.UserLon,
		arg.UserLocationAccuracy,
		arg.CreatedAt,
		arg.SubmittedAt,
	)
	var i ProblemReportsStop
	err := row.Scan(
		&i.ID,
		&i.StopID,
		&i.Code,
		&i.UserComment,
		&i.UserLat,
		&i.UserLon,
		&i.UserLocationAccuracy,
		&i.CreatedAt,
		&i.SubmittedAt,
	)
	return i, err
}

const getProblemReportsByTrip = `
SELECT id, trip_id, service_date, vehicle_id, stop_id, code, user_comment,
    user_lat, user_lon, user_location_accuracy, user_on_vehicle,
    user_vehicle_number, created_at, submitted_at
FROM problem_reports_trip
WHERE trip_id = ?
ORDER BY created_at DESC
`

// GetProblemReportsByTrip returns all problem reports for a specific trip.
func (q *Queries) GetProblemReportsByTrip(ctx context.Context, tripID string) ([]ProblemReportsTrip, error) {
	rows, err := q.db.QueryContext(ctx, getProblemReportsByTrip, tripID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var items []ProblemReportsTrip
	for rows.Next() {
		var i ProblemReportsTrip
		if err := rows.Scan(
			&i.ID,
			&i.TripID,
			&i.ServiceDate,
			&i.VehicleID,
			&i.StopID,
			&i.Code,
			&i.UserComment,
			&i.UserLat,
			&i.UserLon,
			&i.UserLocationAccuracy,
			&i.UserOnVehicle,
			&i.UserVehicleNumber,
			&i.CreatedAt,
			&i.SubmittedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getProblemReportsByStop = `
SELECT id, stop_id, code, user_comment, user_lat, user_lon,
    user_location_accuracy, created_at, submitted_at
FROM problem_reports_stop
WHERE stop_id = ?
ORDER BY created_at DESC
`

// GetProblemReportsByStop returns all problem reports for a specific stop.
func (q *Queries) GetProblemReportsByStop(ctx context.Context, stopID string) ([]ProblemReportsStop, error) {
	rows, err := q.db.QueryContext(ctx, getProblemReportsByStop, stopID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []ProblemReportsStop
	for rows.Next() {
		var i ProblemReportsStop
		if err := rows.Scan(
			&i.ID,
			&i.StopID,
			&i.Code,
			&i.UserComment,
			&i.UserLat,
			&i.UserLon,
			&i.UserLocationAccuracy,
			&i.CreatedAt,
			&i.SubmittedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
