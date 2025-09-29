package models

import "time"

// ResponseModel Base response structure that can be reused
type ResponseModel struct {
	Code        int         `json:"code"`
	CurrentTime int64       `json:"currentTime"`
	Data        interface{} `json:"data,omitempty"`
	Text        string      `json:"text"`
	Version     int         `json:"version"`
}

// NewOKResponse is a helper function that returns a successful response.
func NewOKResponse(data interface{}) ResponseModel {
	return NewResponse(200, data, "OK")
}

func NewListResponse(list interface{}, references ReferencesModel) ResponseModel {
	data := map[string]interface{}{
		"limitExceeded": false,
		"list":          list,
		"references":    references,
	}
	return NewOKResponse(data)
}

func NewListResponseWithRange(list interface{}, references ReferencesModel, outOfRange bool) ResponseModel {
	data := map[string]interface{}{
		"limitExceeded": false,
		"list":          list,
		"outOfRange":    outOfRange,
		"references":    references,
	}
	return NewOKResponse(data)
}

func NewEntryResponse(entry interface{}, references ReferencesModel) ResponseModel {
	data := map[string]interface{}{
		"entry":      entry,
		"references": references,
	}
	return NewOKResponse(data)
}
func NewArrivalsAndDepartureResponse(arrivalsAndDepartures interface{}, references ReferencesModel, nearbyStopIds []string, situationIds []string, stopId string) ResponseModel {
	entryData := map[string]interface{}{
		"arrivalsAndDepartures": arrivalsAndDepartures,
		"nearbyStopIds":         nearbyStopIds,
		"situationIds":          situationIds,
		"stopId":                stopId,
	}
	data := map[string]interface{}{
		"entry":      entryData,
		"references": references,
	}
	return NewOKResponse(data)
}

// NewResponse Helper function to create a standard response
func NewResponse(code int, data interface{}, text string) ResponseModel {
	return ResponseModel{
		Code:        code,
		CurrentTime: ResponseCurrentTime(),
		Data:        data,
		Text:        text,
		Version:     2,
	}
}

func ResponseCurrentTime() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}
