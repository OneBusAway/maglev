package models

import (
	"maglev.onebusaway.org/gtfsdb"
)

// AgencyCoverage represents the geographical coverage area of a transit agency
type AgencyCoverage struct {
	AgencyID string  `json:"agencyId"`
	Lat      float64 `json:"lat"`
	LatSpan  float64 `json:"latSpan"`
	Lon      float64 `json:"lon"`
	LonSpan  float64 `json:"lonSpan"`
}

// NewAgencyCoverage creates a new AgencyCoverage instance with the provided values
func NewAgencyCoverage(agencyID string, lat, latSpan, lon, lonSpan float64) AgencyCoverage {
	return AgencyCoverage{
		AgencyID: agencyID,
		Lat:      lat,
		LatSpan:  latSpan,
		Lon:      lon,
		LonSpan:  lonSpan,
	}
}

type AgencyReference struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Timezone       string `json:"timezone"`
	Lang           string `json:"lang"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	FareUrl        string `json:"fareUrl"`
	Disclaimer     string `json:"disclaimer"`
	PrivateService bool   `json:"privateService"`
}

// NewAgencyReference creates a new AgencyReference instance with the provided values
func NewAgencyReference(id, name, url, timezone, lang, phone, email, fareUrl, disclaimer string, privateService bool) AgencyReference {
	return AgencyReference{
		ID:             id,
		Name:           name,
		URL:            url,
		Timezone:       timezone,
		Lang:           lang,
		Phone:          phone,
		Email:          email,
		FareUrl:        fareUrl,
		Disclaimer:     disclaimer,
		PrivateService: privateService,
	}
}

func AgencyReferenceFromDatabase(agency *gtfsdb.Agency) AgencyReference {
	return AgencyReference{
		ID:             agency.ID,
		Name:           agency.Name,
		URL:            agency.Url,
		Timezone:       agency.Timezone,
		Lang:           gtfsdb.NullStringOrEmpty(agency.Lang),
		Phone:          gtfsdb.NullStringOrEmpty(agency.Phone),
		Email:          gtfsdb.NullStringOrEmpty(agency.Email),
		FareUrl:        gtfsdb.NullStringOrEmpty(agency.FareUrl),
		Disclaimer:     "",
		PrivateService: false,
	}
}
