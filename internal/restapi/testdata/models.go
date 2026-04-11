package testdata

import "maglev.onebusaway.org/internal/models"

var Raba = models.AgencyReference{
	Disclaimer:     "",
	Email:          "",
	FareUrl:        "",
	ID:             "25",
	Lang:           "en",
	Name:           "Redding Area Bus Authority",
	Phone:          "530-241-2877",
	PrivateService: false,
	Timezone:       "America/Los_Angeles",
	URL:            "http://www.rabaride.com/",
}

var Route19 = models.Route{
	AgencyID:          "25",
	Color:             "2738ec",
	Description:       "",
	ID:                "25_3779",
	LongName:          "Route 19",
	NullSafeShortName: "19",
	ShortName:         "19",
	TextColor:         "ffffff",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/SRTA_BeachBus_20230302_ful.pdf",
}

var Route44x = models.Route{
	AgencyID:          "25",
	Color:             "F84DF7",
	Description:       "",
	ID:                "25_44X",
	LongName:          "Shingletown Flex",
	NullSafeShortName: "44X",
	ShortName:         "44X",
	TextColor:         "000000",
	Type:              3,
	URL:               "",
}

var Route15 = models.Route{
	AgencyID:          "25",
	Color:             "800000",
	Description:       "",
	ID:                "25_15",
	LongName:          "Churn Creek/Knightson/Airport",
	NullSafeShortName: "15",
	ShortName:         "15",
	TextColor:         "FFFFFF",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/Bus/15-18.pdf",
}

var Route14 = models.Route{
	AgencyID:          "25",
	Color:             "8c08b1",
	Description:       "",
	ID:                "25_160",
	LongName:          "Route 14",
	NullSafeShortName: "14",
	ShortName:         "14",
	TextColor:         "ffffff",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/Bus/Route14.pdf",
}

var Route11 = models.Route{
	AgencyID:          "25",
	Color:             "93ea5f",
	Description:       "",
	ID:                "25_159",
	LongName:          "Route 11",
	NullSafeShortName: "11",
	ShortName:         "11",
	TextColor:         "000000",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/Bus/Route11.pdf",
}
