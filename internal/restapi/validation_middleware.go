package restapi

import (
	"fmt"
	"net/http"
	"strconv"
)

// QueryParamRule defines a validation rule for a single query parameter.
// If the parameter is absent from the request, the rule is skipped (optional params).
// The Validate function receives the raw string value and returns an error message
// and a boolean indicating whether validation passed.
type QueryParamRule struct {
	Param    string
	Validate func(value string) (errMsg string, ok bool)
}

// PositiveIntRule returns a rule that validates a query parameter is a positive integer (> 0).
func PositiveIntRule(param string) QueryParamRule {
	return QueryParamRule{
		Param: param,
		Validate: func(value string) (string, bool) {
			n, err := strconv.Atoi(value)
			if err != nil {
				return "must be a valid integer", false
			}
			if n <= 0 {
				return "must be a positive integer", false
			}
			return "", true
		},
	}
}

// IntRangeRule returns a rule that validates a query parameter is an integer within [min, max].
func IntRangeRule(param string, min, max int) QueryParamRule {
	return QueryParamRule{
		Param: param,
		Validate: func(value string) (string, bool) {
			n, err := strconv.Atoi(value)
			if err != nil {
				return "must be a valid integer", false
			}
			if n < min || n > max {
				return fmt.Sprintf("must be between %d and %d", min, max), false
			}
			return "", true
		},
	}
}

// NonNegativeIntRule returns a rule that validates a query parameter is a non-negative integer (>= 0).
func NonNegativeIntRule(param string) QueryParamRule {
	return QueryParamRule{
		Param: param,
		Validate: func(value string) (string, bool) {
			n, err := strconv.Atoi(value)
			if err != nil {
				return "must be a valid integer", false
			}
			if n < 0 {
				return "must be a non-negative integer", false
			}
			return "", true
		},
	}
}

// ValidateQueryParams applies validation rules to query parameters
// and returns 400 if validation fails before invoking the next handler.
// Parameters not present in the request are skipped (all rules are optional-param-safe).
func ValidateQueryParams(api *RestAPI, rules []QueryParamRule, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		fieldErrors := make(map[string][]string)

		for _, rule := range rules {
			value := query.Get(rule.Param)
			if value == "" {
				continue // param not provided, skip
			}
			if errMsg, ok := rule.Validate(value); !ok {
				fieldErrors[rule.Param] = append(fieldErrors[rule.Param], errMsg)
			}
		}

		if len(fieldErrors) > 0 {
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}

		next.ServeHTTP(w, r)
	})
}
