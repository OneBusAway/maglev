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

var Route1 = models.Route{
	AgencyID:          "25",
	Color:             "55d1b0",
	Description:       "",
	ID:                "25_151",
	LongName:          "Route 1",
	NullSafeShortName: "1",
	ShortName:         "1",
	TextColor:         "000000",
	Type:              3,
	URL:               "https://rabaride.com/Document%20Center/Services/Bus/Route01.pdf",
}

var Route299x = models.Route{
	AgencyID:          "25",
	Color:             "00dffb",
	Description:       "",
	ID:                "25_161",
	LongName:          "Route 299X",
	NullSafeShortName: "299X",
	ShortName:         "299X",
	TextColor:         "ffffff",
	Type:              3,
	URL:               "https://rabaride.com/services/burney_express.php",
}

var Route17 = models.Route{
	AgencyID:          "25",
	Color:             "fbef00",
	Description:       "",
	ID:                "25_1885",
	LongName:          "Shasta View/Shasta College",
	NullSafeShortName: "17",
	ShortName:         "17",
	TextColor:         "0e0e0e",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/17.pdf",
}

var Route3 = models.Route{
	AgencyID:          "25",
	Color:             "1bb1f6",
	Description:       "",
	ID:                "25_153",
	LongName:          "Route 3",
	NullSafeShortName: "3",
	ShortName:         "3",
	TextColor:         "ffffff",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/Bus/Route03.pdf",
}

var Route4 = models.Route{
	AgencyID:          "25",
	Color:             "bdadd1",
	Description:       "",
	ID:                "25_154",
	LongName:          "Route 4",
	NullSafeShortName: "4",
	ShortName:         "4",
	TextColor:         "ffffff",
	Type:              3,
	URL:               "https://rabaride.com/Document%20Center/Services/Bus/Route04.pdf",
}

var Route7 = models.Route{
	AgencyID:          "25",
	Color:             "1886c7",
	Description:       "",
	ID:                "25_157",
	LongName:          "Route 7",
	NullSafeShortName: "7",
	ShortName:         "7",
	TextColor:         "ffffff",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/Bus/Route07.pdf",
}

var Route9 = models.Route{
	AgencyID:          "25",
	Color:             "ec191c",
	Description:       "",
	ID:                "25_6446",
	LongName:          "Route 9",
	NullSafeShortName: "9",
	ShortName:         "9",
	TextColor:         "000000",
	Type:              3,
	URL:               "https://cms3.revize.com/revize/reddingbusauthority/Document%20Center/Services/Bus/Route09.pdf",
}

var Route99x = models.Route{
	AgencyID:          "25",
	Color:             "9F472E",
	Description:       "",
	ID:                "25_24",
	LongName:          "Route 99X/Amtrak Thruway Route 3",
	NullSafeShortName: "99X",
	ShortName:         "99X",
	TextColor:         "FFFFFF",
	Type:              3,
	URL:               "",
}

var RabaRoutes = []models.Route{
	Route1,
	Route3,
	Route4,
	Route7,
	Route9,
	Route11,
	Route14,
	Route15,
	Route17,
	Route19,
	Route44x,
	Route99x,
	Route299x,
}

var Stop4062 = models.Stop{
	Direction:          "SE",
	ID:                 "25_4062",
	Lat:                40.539367,
	Lon:                -122.34952,
	Name:               "Churn Creek Rd at Hillmonte Dr (FS)",
	RouteIDs:           []string{"25_154"},
	StaticRouteIDs:     []string{"25_154"},
	WheelchairBoarding: "UNKNOWN",
}
