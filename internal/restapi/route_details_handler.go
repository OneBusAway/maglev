package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// routeDetailsHandler returns canonical/ideal patterns and shapes for a specific route.
func (api *RestAPI) routeDetailsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}
	rawID := r.PathValue("id")
	if idx := strings.Index(rawID, "_"); idx <= 0 || idx == len(rawID)-1 {
		api.sendNotFound(w, r)
		return
	}

	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			api.sendNotFound(w, r)
			return
		}
		api.serverErrorResponse(w, r, err)
		return
	}

	formattedDate, filterByDate, ok := api.parseDateForRouteDetails(w, r, currentAgency.ID, currentAgency.Timezone)
	if !ok {
		return
	}

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			api.sendNotFound(w, r)
			return
		}
		api.serverErrorResponse(w, r, err)
		return
	}
	if route.ID == "" {
		api.sendNotFound(w, r)
		return
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	routeEntry, stopsList, err := api.processRouteStops(ctx, agencyID, routeID, serviceIDs, filterByDate, false)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	result := models.NewRouteDetailsEntry(
		models.NewAgencyAndId(agencyID, routeID),
		routeEntry.StopGroupings,
	)

	references, err := api.assembleRouteDetailsReferences(ctx, agencyID, currentAgency, route, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	response := models.NewListResponse([]models.RouteDetailsEntry{result}, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}

func (api *RestAPI) parseDateForRouteDetails(w http.ResponseWriter, r *http.Request, agencyID, timezone string) (string, bool, bool) {
	currentLocation, err := loadAgencyLocation(agencyID, timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return "", false, false
	}

	timeParam := r.URL.Query().Get("time")
	if timeParam == "" {
		timeParam = r.URL.Query().Get("serviceDate")
	}
	filterByDate := timeParam != ""

	formattedDate, _, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return "", false, false
	}

	return formattedDate, filterByDate, true
}

func (api *RestAPI) assembleRouteDetailsReferences(ctx context.Context, agencyID string, currentAgency gtfsdb.Agency, route gtfsdb.Route, stopsList []models.Stop) (*models.ReferencesModel, error) {
	agencyRef := models.NewAgencyReference(
		currentAgency.ID,
		currentAgency.Name,
		currentAgency.Url,
		currentAgency.Timezone,
		currentAgency.Lang.String,
		currentAgency.Phone.String,
		currentAgency.Email.String,
		currentAgency.FareUrl.String,
		"",
		false,
	)

	routes, err := api.BuildRouteReferences(ctx, currentAgency.ID, stopsList)
	if err != nil {
		return nil, err
	}

	routeData := models.NewRoute(
		utils.FormCombinedID(agencyID, route.ID),
		agencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String)

	routeInRefs := false
	for _, ref := range routes {
		if ref.ID == routeData.ID {
			routeInRefs = true
			break
		}
	}
	if !routeInRefs {
		routes = append(routes, routeData)
	}

	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{agencyRef}
	references.Routes = routes
	references.Stops = stopsList

	alerts := api.GtfsManager.GetAlertsForRoute(route.ID)
	situations := api.BuildSituationReferences(alerts)
	references.Situations = append(references.Situations, situations...)

	return references, nil
}
