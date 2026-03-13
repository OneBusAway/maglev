package restapi

import (
	"fmt"
	"time"
)

func loadAgencyLocation(agencyID, timezone string) (*time.Location, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone for agency %q: %w", agencyID, err)
	}
	return loc, nil
}
