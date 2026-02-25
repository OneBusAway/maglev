package restapi

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

const (
	defaultProblemReportsLimit = 50
	maxProblemReportsLimit     = 100
)

// problemReportEntry is an internal struct used to merge and sort trip and stop
// reports before pagination.
type problemReportEntry struct {
	createdAt int64
	item      models.ProblemReportItem
}

// problemReportsHandler handles GET /api/where/problem-reports.json.
//
// Query parameters:
//   - startDate: Unix timestamp in milliseconds. Filter reports created >= startDate.
//   - endDate:   Unix timestamp in milliseconds. Filter reports created <= endDate.
//   - code:      Problem code string. Filter reports by this code.
//   - limit:     Maximum number of reports to return (default 50, max 100).
//   - offset:    Number of results to skip for pagination (default 0).
//
// When both startDate and endDate are supplied they take precedence. When only
// code is supplied it takes precedence over the default (no filter) behaviour.
func (api *RestAPI) problemReportsHandler(w http.ResponseWriter, r *http.Request) {
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		api.sendError(w, r, http.StatusInternalServerError, "internal server error")
		return
	}

	q := r.URL.Query()

	// Parse pagination parameters.
	limit := int64(defaultProblemReportsLimit)
	if limitStr := q.Get("limit"); limitStr != "" {
		if parsed, err := strconv.ParseInt(limitStr, 10, 64); err == nil && parsed > 0 {
			limit = parsed
			if limit > maxProblemReportsLimit {
				limit = maxProblemReportsLimit
			}
		}
	}

	offset := int64(0)
	if offsetStr := q.Get("offset"); offsetStr != "" {
		if parsed, err := strconv.ParseInt(offsetStr, 10, 64); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Parse filter parameters.
	code := q.Get("code")
	startDateStr := q.Get("startDate")
	endDateStr := q.Get("endDate")

	var startDate, endDate int64
	hasDateRange := false
	if startDateStr != "" && endDateStr != "" {
		if s, err := strconv.ParseInt(startDateStr, 10, 64); err == nil {
			startDate = s
		}
		if e, err := strconv.ParseInt(endDateStr, 10, 64); err == nil {
			endDate = e
		}
		hasDateRange = startDate > 0 && endDate > 0
	}

	ctx := r.Context()
	queries := api.GtfsManager.GtfsDB.Queries

	// Fetch one more than needed (limit + offset + 1) from each table to detect
	// whether more results exist beyond the requested page.
	fetchSize := limit + offset + 1

	var tripReports []gtfsdb.ProblemReportsTrip
	var stopReports []gtfsdb.ProblemReportsStop
	var tripErr, stopErr error

	switch {
	case hasDateRange:
		tripReports, tripErr = queries.GetProblemReportsTripByDateRange(ctx, gtfsdb.GetProblemReportsTripByDateRangeParams{
			CreatedAt:   startDate,
			CreatedAt_2: endDate,
			Limit:       fetchSize,
			Offset:      0,
		})
		stopReports, stopErr = queries.GetProblemReportsStopByDateRange(ctx, gtfsdb.GetProblemReportsStopByDateRangeParams{
			CreatedAt:   startDate,
			CreatedAt_2: endDate,
			Limit:       fetchSize,
			Offset:      0,
		})
	case code != "":
		nullCode := sql.NullString{String: code, Valid: true}
		tripReports, tripErr = queries.GetProblemReportsTripByCode(ctx, gtfsdb.GetProblemReportsTripByCodeParams{
			Code:   nullCode,
			Limit:  fetchSize,
			Offset: 0,
		})
		stopReports, stopErr = queries.GetProblemReportsStopByCode(ctx, gtfsdb.GetProblemReportsStopByCodeParams{
			Code:   nullCode,
			Limit:  fetchSize,
			Offset: 0,
		})
	default:
		tripReports, tripErr = queries.GetRecentProblemReportsTrip(ctx, gtfsdb.GetRecentProblemReportsTripParams{
			Limit:  fetchSize,
			Offset: 0,
		})
		stopReports, stopErr = queries.GetRecentProblemReportsStop(ctx, gtfsdb.GetRecentProblemReportsStopParams{
			Limit:  fetchSize,
			Offset: 0,
		})
	}

	if tripErr != nil {
		api.serverErrorResponse(w, r, tripErr)
		return
	}
	if stopErr != nil {
		api.serverErrorResponse(w, r, stopErr)
		return
	}

	// Build the unified list for sorting.
	combined := make([]problemReportEntry, 0, len(tripReports)+len(stopReports))
	for i := range tripReports {
		m := models.NewProblemReportTrip(tripReports[i])
		combined = append(combined, problemReportEntry{
			createdAt: tripReports[i].CreatedAt,
			item: models.ProblemReportItem{
				ReportType: "trip",
				TripReport: &m,
			},
		})
	}
	for i := range stopReports {
		m := models.NewProblemReportStop(stopReports[i])
		combined = append(combined, problemReportEntry{
			createdAt: stopReports[i].CreatedAt,
			item: models.ProblemReportItem{
				ReportType: "stop",
				StopReport: &m,
			},
		})
	}

	// Sort combined list by created_at descending (most recent first).
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].createdAt > combined[j].createdAt
	})

	// Determine whether more results exist beyond the requested page.
	total := int64(len(combined))
	limitExceeded := total > offset+limit

	if offset >= total {
		combined = combined[:0]
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		combined = combined[offset:end]
	}

	// Build the paginated list.
	items := make([]models.ProblemReportItem, len(combined))
	for i := range combined {
		items[i] = combined[i].item
	}

	references := models.NewEmptyReferences()
	response := models.NewListResponse(items, references, limitExceeded, api.Clock)
	api.sendResponse(w, r, response)
}
