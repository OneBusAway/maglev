package models

import (
	"encoding/json"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/clock"
)

func TestCurrentTimeModel(t *testing.T) {
	// Create a sample CurrentTimeModel
	timeModel := CurrentTimeModel{
		ReadableTime: "2025-05-03T12:00:00Z",
		Time:         1746324000000, // Unix time in milliseconds
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(timeModel)
	if err != nil {
		t.Fatalf("Failed to marshal CurrentTimeModel to JSON: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledModel CurrentTimeModel
	err = json.Unmarshal(jsonData, &unmarshaledModel)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to CurrentTimeModel: %v", err)
	}

	// Verify fields were preserved correctly
	if unmarshaledModel.ReadableTime != timeModel.ReadableTime {
		t.Errorf("Expected ReadableTime %s, got %s",
			timeModel.ReadableTime, unmarshaledModel.ReadableTime)
	}

	if unmarshaledModel.Time != timeModel.Time {
		t.Errorf("Expected Time %d, got %d",
			timeModel.Time, unmarshaledModel.Time)
	}
}

func TestCurrentTimeData(t *testing.T) {
	// Create a sample CurrentTimeData
	entry := CurrentTimeModel{
		ReadableTime: "2025-05-03T12:00:00Z",
		Time:         1746324000000,
	}

	references := NewEmptyReferences()

	timeData := CurrentTimeData{
		Entry:      entry,
		References: references,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(timeData)
	if err != nil {
		t.Fatalf("Failed to marshal CurrentTimeData to JSON: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledData CurrentTimeData
	err = json.Unmarshal(jsonData, &unmarshaledData)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to CurrentTimeData: %v", err)
	}

	// Verify fields were preserved correctly
	if unmarshaledData.Entry.ReadableTime != timeData.Entry.ReadableTime {
		t.Errorf("Expected Entry.ReadableTime %s, got %s",
			timeData.Entry.ReadableTime, unmarshaledData.Entry.ReadableTime)
	}

	if unmarshaledData.Entry.Time != timeData.Entry.Time {
		t.Errorf("Expected Entry.Time %d, got %d",
			timeData.Entry.Time, unmarshaledData.Entry.Time)
	}

	// Verify references
	if len(unmarshaledData.References.Agencies) != 0 {
		t.Errorf("Expected empty Agencies, got %d items", len(unmarshaledData.References.Agencies))
	}

	if len(unmarshaledData.References.Routes) != 0 {
		t.Errorf("Expected empty Routes, got %d items", len(unmarshaledData.References.Routes))
	}

	// We could continue checking other reference fields, but these are sufficient
}

func TestNewCurrentTimeData(t *testing.T) {
	// Test cases with different times
	testCases := []struct {
		name     string
		testTime time.Time
	}{
		{
			name:     "UTC Time",
			testTime: time.Date(2025, 5, 3, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "Local Time",
			testTime: time.Date(2025, 5, 3, 12, 0, 0, 0, time.Local),
		},
		{
			name:     "Zero Time",
			testTime: time.Time{}, // Zero value
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Expected values
			expectedMillis := tc.testTime.UnixNano() / int64(time.Millisecond)
			expectedReadable := tc.testTime.Format(time.RFC3339)

			// Call the function being tested
			result := NewCurrentTimeData(tc.testTime)

			// Verify the time fields
			if result.Entry.Time != expectedMillis {
				t.Errorf("Expected time %d, got %d", expectedMillis, result.Entry.Time)
			}

			if result.Entry.ReadableTime != expectedReadable {
				t.Errorf("Expected readable time %s, got %s",
					expectedReadable, result.Entry.ReadableTime)
			}

			// Verify that references is initialized
			if result.References.Agencies == nil {
				t.Error("References.Agencies should be initialized, not nil")
			}

			if len(result.References.Agencies) != 0 {
				t.Errorf("Expected empty References.Agencies, got %d items",
					len(result.References.Agencies))
			}

			// We could check other reference fields, but this is sufficient
		})
	}
}

func TestCurrentTimeDataEndToEnd(t *testing.T) {
	// Create a fixed test time
	testTime := time.Date(2025, 5, 3, 12, 0, 0, 0, time.UTC)

	// Create the data using our function
	timeData := NewCurrentTimeData(testTime)

	clock := clock.NewMockClock(testTime)

	// Create a response using this data
	response := NewResponse(200, timeData, "OK", clock)

	// Marshal to JSON
	jsonData, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal response to JSON: %v", err)
	}

	// Unmarshal back to verify structure
	var result map[string]interface{}
	err = json.Unmarshal(jsonData, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Check top-level structure
	if code, ok := result["code"].(float64); !ok || int(code) != 200 {
		t.Errorf("Expected code 200, got %v", result["code"])
	}

	if text, ok := result["text"].(string); !ok || text != "OK" {
		t.Errorf("Expected text 'OK', got %v", result["text"])
	}

	if version, ok := result["version"].(float64); !ok || int(version) != 2 {
		t.Errorf("Expected version 2, got %v", result["version"])
	}

	// Check data structure
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data to be an object, got %T", result["data"])
	}

	// Check entry
	entry, ok := data["entry"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected entry to be an object, got %T", data["entry"])
	}

	if timeValue, ok := entry["time"].(float64); !ok {
		t.Errorf("Expected time to be a number, got %T", entry["time"])
	} else {
		expectedMillis := testTime.UnixNano() / int64(time.Millisecond)
		if int64(timeValue) != expectedMillis {
			t.Errorf("Expected time %d, got %d", expectedMillis, int64(timeValue))
		}
	}

	if readableTime, ok := entry["readableTime"].(string); !ok {
		t.Errorf("Expected readableTime to be a string, got %T", entry["readableTime"])
	} else {
		expectedReadable := testTime.Format(time.RFC3339)
		if readableTime != expectedReadable {
			t.Errorf("Expected readableTime %s, got %s", expectedReadable, readableTime)
		}
	}

	// Check references
	references, ok := data["references"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected references to be an object, got %T", data["references"])
	}

	// Check that all reference arrays are present and empty
	referenceFields := []string{"agencies", "routes", "situations", "stopTimes", "stops", "trips"}
	for _, field := range referenceFields {
		arr, ok := references[field].([]interface{})
		if !ok {
			t.Errorf("Expected %s to be an array, got %T", field, references[field])
		} else if len(arr) != 0 {
			t.Errorf("Expected %s to be empty, got %d items", field, len(arr))
		}
	}
}
