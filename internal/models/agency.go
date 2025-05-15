package models

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
	Disclaimer     string `json:"disclaimer"`
	Email          string `json:"email"`
	FareUrl        string `json:"fareUrl"`
	ID             string `json:"id"`
	Lang           string `json:"lang"`
	Name           string `json:"name"`
	Phone          string `json:"phone"`
	PrivateService bool   `json:"privateService"`
	Timezone       string `json:"timezone"`
	URL            string `json:"url"`
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
