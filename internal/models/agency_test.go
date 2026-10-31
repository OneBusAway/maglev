package models

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
)

func TestAgencyCoverageCreation(t *testing.T) {
	agencyID := "agency-1"
	lat := 47.6062
	latSpan := 0.5
	lon := -122.3321
	lonSpan := 0.8

	coverage := NewAgencyCoverage(agencyID, lat, latSpan, lon, lonSpan)

	assert.Equal(t, agencyID, coverage.AgencyID)
	assert.Equal(t, lat, coverage.Lat)
	assert.Equal(t, latSpan, coverage.LatSpan)
	assert.Equal(t, lon, coverage.Lon)
	assert.Equal(t, lonSpan, coverage.LonSpan)
}

func TestAgencyCoverageJSON(t *testing.T) {
	coverage := AgencyCoverage{
		AgencyID: "agency-2",
		Lat:      47.6062,
		LatSpan:  0.5,
		Lon:      -122.3321,
		LonSpan:  0.8,
	}

	jsonData, err := json.Marshal(coverage)
	assert.NoError(t, err)

	var unmarshaledCoverage AgencyCoverage
	err = json.Unmarshal(jsonData, &unmarshaledCoverage)
	assert.NoError(t, err)

	assert.Equal(t, coverage.AgencyID, unmarshaledCoverage.AgencyID)
	assert.Equal(t, coverage.Lat, unmarshaledCoverage.Lat)
	assert.Equal(t, coverage.LatSpan, unmarshaledCoverage.LatSpan)
	assert.Equal(t, coverage.Lon, unmarshaledCoverage.Lon)
	assert.Equal(t, coverage.LonSpan, unmarshaledCoverage.LonSpan)
}

func TestAgencyReferenceCreation(t *testing.T) {
	id := "st"
	name := "Sound Transit"
	url := "https://soundtransit.org"
	timezone := "America/Los_Angeles"
	lang := "en"
	phone := "530-241-2877"
	email := "example@soundtransit.org"
	fareUrl := "https://soundtransit.org/fares"
	disclaimer := "Transit data provided by Sound Transit"
	privateService := false

	agency := NewAgencyReference(id, name, url, timezone, lang, phone, email, fareUrl, disclaimer, privateService)

	assert.Equal(t, id, agency.ID)
	assert.Equal(t, name, agency.Name)
	assert.Equal(t, url, agency.URL)
	assert.Equal(t, timezone, agency.Timezone)
	assert.Equal(t, lang, agency.Lang)
	assert.Equal(t, phone, agency.Phone)
	assert.Equal(t, email, agency.Email)
	assert.Equal(t, fareUrl, agency.FareUrl)
	assert.Equal(t, disclaimer, agency.Disclaimer)
	assert.Equal(t, privateService, agency.PrivateService)
}

func TestAgencyReferenceJSON(t *testing.T) {
	agency := AgencyReference{
		ID:             "st",
		Name:           "Sound Transit",
		URL:            "https://soundtransit.org",
		Timezone:       "America/Los_Angeles",
		Lang:           "en",
		Phone:          "530-241-2877",
		Email:          "example@soundtransit.org",
		FareUrl:        "https://soundtransit.org/fares",
		Disclaimer:     "Transit data provided by Sound Transit",
		PrivateService: true,
	}

	jsonData, err := json.Marshal(agency)
	assert.NoError(t, err)

	var unmarshaledAgency AgencyReference
	err = json.Unmarshal(jsonData, &unmarshaledAgency)
	assert.NoError(t, err)

	assert.Equal(t, agency.ID, unmarshaledAgency.ID)
	assert.Equal(t, agency.Name, unmarshaledAgency.Name)
	assert.Equal(t, agency.URL, unmarshaledAgency.URL)
	assert.Equal(t, agency.Timezone, unmarshaledAgency.Timezone)
	assert.Equal(t, agency.Lang, unmarshaledAgency.Lang)
	assert.Equal(t, agency.Phone, unmarshaledAgency.Phone)
	assert.Equal(t, agency.Email, unmarshaledAgency.Email)
	assert.Equal(t, agency.FareUrl, unmarshaledAgency.FareUrl)
	assert.Equal(t, agency.Disclaimer, unmarshaledAgency.Disclaimer)
	assert.Equal(t, agency.PrivateService, unmarshaledAgency.PrivateService)
}

func TestAgencyCoverageWithZeroValues(t *testing.T) {
	coverage := NewAgencyCoverage("agency-3", 0, 0, 0, 0)

	assert.Equal(t, "agency-3", coverage.AgencyID)
	assert.Equal(t, 0.0, coverage.Lat)
	assert.Equal(t, 0.0, coverage.LatSpan)
	assert.Equal(t, 0.0, coverage.Lon)
	assert.Equal(t, 0.0, coverage.LonSpan)
}

func TestAgencyReferenceWithEmptyStrings(t *testing.T) {
	agency := NewAgencyReference("", "", "", "", "", "", "", "", "", false)

	assert.Equal(t, "", agency.ID)
	assert.Equal(t, "", agency.Name)
	assert.Equal(t, "", agency.URL)
	assert.Equal(t, "", agency.Timezone)
	assert.Equal(t, "", agency.Lang)
	assert.Equal(t, "", agency.Phone)
	assert.Equal(t, "", agency.Email)
	assert.Equal(t, "", agency.FareUrl)
	assert.Equal(t, "", agency.Disclaimer)
	assert.Equal(t, false, agency.PrivateService)
}

func TestAgencyReferenceFromDatabase_Nil(t *testing.T) {
	ref := AgencyReferenceFromDatabase(nil)
	assert.Equal(t, AgencyReference{}, ref)
}

func TestAgencyReferenceFromDatabase_Valid(t *testing.T) {
	agency := &gtfsdb.Agency{
		ID:       "agency-1",
		Name:     "Metro Transit",
		Url:      "https://metro.example.com",
		Timezone: "America/New_York",
		Lang:     sql.NullString{String: "en", Valid: true},
		Phone:    sql.NullString{String: "555-1234", Valid: true},
		Email:    sql.NullString{String: "info@metro.example.com", Valid: true},
		FareUrl:  sql.NullString{String: "https://metro.example.com/fare", Valid: true},
	}
	ref := AgencyReferenceFromDatabase(agency)
	assert.Equal(t, "agency-1", ref.ID)
	assert.Equal(t, "Metro Transit", ref.Name)
	assert.Equal(t, "https://metro.example.com", ref.URL)
	assert.Equal(t, "America/New_York", ref.Timezone)
	assert.Equal(t, "en", ref.Lang)
	assert.Equal(t, "555-1234", ref.Phone)
	assert.Equal(t, "info@metro.example.com", ref.Email)
	assert.Equal(t, "https://metro.example.com/fare", ref.FareUrl)
	assert.Equal(t, "", ref.Disclaimer)
	assert.False(t, ref.PrivateService)
}
