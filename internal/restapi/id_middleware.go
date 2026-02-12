package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/utils"
)

// ValidateIDMiddleware extracts the {id} param, validates it against safety rules,
// and injects it into the context. If validation fails, it returns 400.
func (api *RestAPI) ValidateIDMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := utils.ExtractIDFromParams(r)

		if err := utils.ValidateID(id); err != nil {
			fieldErrors := map[string][]string{
				"id": {err.Error()},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}

		ctx := utils.WithValidatedID(r.Context(), id)
		next(w, r.WithContext(ctx))
	}
}

// ValidateCombinedIDMiddleware enforces that the ID is in "agency_code" format.
// It injects both the raw ID and the ParsedID struct into the context.
func (api *RestAPI) ValidateCombinedIDMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := utils.ExtractIDFromParams(r)

		// Basic Validation
		if err := utils.ValidateID(id); err != nil {
			fieldErrors := map[string][]string{
				"id": {err.Error()},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}

		// Parse Split
		agencyID, codeID, err := utils.ExtractAgencyIDAndCodeID(id)
		if err != nil {
			fieldErrors := map[string][]string{
				"id": {"invalid format, expected agency_id"},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}

		// Inject into Context
		parsed := utils.ParsedID{
			CombinedID: id,
			AgencyID:   agencyID,
			CodeID:     codeID,
		}
		ctx := utils.WithParsedID(r.Context(), parsed)

		// Also add the raw ID for convenience
		ctx = utils.WithValidatedID(ctx, id)

		next(w, r.WithContext(ctx))
	}
}
