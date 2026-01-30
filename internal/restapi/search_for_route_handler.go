package restapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) searchForRouteHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()
	input := queryParams.Get("input")

	maxCount := 20
	if val := queryParams.Get("maxCount"); val != "" {
		if i, err := strconv.Atoi(val); err == nil && i > 0 {
			maxCount = i
		}
	}

	if input == "" {
		api.validationErrorResponse(w, r, map[string][]string{"input": {"required"}})
		return
	}

	inputLower := strings.ToLower(input)
	var results = []models.Route{}

	// Access static GTFS data
	allRoutes := api.GtfsManager.GetStaticData().Routes

	for _, route := range allRoutes {
		// safe null checks if needed, but go-gtfs usually has values or empty strings
		shortName := route.ShortName
		longName := route.LongName

		match := false
		if strings.Contains(strings.ToLower(shortName), inputLower) {
			match = true
		} else if strings.Contains(strings.ToLower(longName), inputLower) {
			match = true
		}

		if match {
			// Convert gtfs.Route to models.Route
			// Note: Agency is a struct in gtfs.Route, we need Agency.Id
			results = append(results, models.NewRoute(
				utils.FormCombinedID(route.Agency.Id, route.Id),
				route.Agency.Id,
				shortName,
				longName,
				route.Description,
				models.RouteType(route.Type),
				route.Url,
				route.Color,
				route.TextColor,
				shortName, // Use ShortName as Name
			))
		}
	}

	// Sort results: matches starting with input first, then alphabetical
	sort.Slice(results, func(i, j int) bool {
		nameI := strings.ToLower(results[i].ShortName)
		nameJ := strings.ToLower(results[j].ShortName)

		startsWithI := strings.HasPrefix(nameI, inputLower)
		startsWithJ := strings.HasPrefix(nameJ, inputLower)

		if startsWithI && !startsWithJ {
			return true
		}
		if !startsWithI && startsWithJ {
			return false
		}
		return nameI < nameJ
	})

	// Limit results
	if len(results) > maxCount {
		results = results[:maxCount]
	}

	// Convert GTFS agencies to AgencyReference
	gtfsAgencies := api.GtfsManager.GetAgencies()
	agencyRefs := make([]models.AgencyReference, len(gtfsAgencies))
	for i, a := range gtfsAgencies {
		agencyRefs[i] = models.AgencyReference{
			ID:             a.Id,
			Name:           a.Name,
			URL:            a.Url,
			Timezone:       a.Timezone,
			Lang:           "en", // Defaulting to en as Lang might be missing in struct
			Phone:          a.Phone,
			FareUrl:        a.FareUrl,
			PrivateService: false, // Default to false
		}
	}

	references := models.ReferencesModel{
		Agencies:   agencyRefs,
		Routes:     []interface{}{},
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	response := models.NewListResponseWithRange(results, references, len(results) == 0)
	api.sendResponse(w, r, response)
}
