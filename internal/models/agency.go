package models

import (
	"database/sql"

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
	Lang           string `json:"lang,omitempty"`
	Phone          string `json:"phone,omitempty"`
	Email          string `json:"email,omitempty"`
	FareUrl        string `json:"fareUrl,omitempty"`
	Disclaimer     string `json:"disclaimer,omitempty"`
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

// nullStringOrEmpty safely extracts a string from sql.NullString, returning "" if invalid.
func nullStringOrEmpty(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func AgencyReferenceFromDatabase(agency *gtfsdb.Agency) AgencyReference {
	return AgencyReference{
		ID:             agency.ID,
		Name:           agency.Name,
		URL:            agency.Url,
		Timezone:       agency.Timezone,
		Lang:           nullStringOrEmpty(agency.Lang),
		Phone:          nullStringOrEmpty(agency.Phone),
		Email:          nullStringOrEmpty(agency.Email),
		FareUrl:        nullStringOrEmpty(agency.FareUrl),
		Disclaimer:     "",
		PrivateService: false,
	}
}
