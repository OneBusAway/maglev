package models

import (
	"encoding/json"
	"testing"
)

func TestNewEmptyReferences(t *testing.T) {
	// Call the function to create an empty references model
	refs := NewEmptyReferences()

	// Check that all slices are initialized (not nil)
	if refs.Agencies == nil {
		t.Error("Agencies slice should be initialized, not nil")
	}
	if refs.Routes == nil {
		t.Error("Routes slice should be initialized, not nil")
	}
	if refs.Situations == nil {
		t.Error("Situations slice should be initialized, not nil")
	}
	if refs.StopTimes == nil {
		t.Error("StopTimes slice should be initialized, not nil")
	}
	if refs.Stops == nil {
		t.Error("Stops slice should be initialized, not nil")
	}
	if refs.Trips == nil {
		t.Error("Trips slice should be initialized, not nil")
	}

	// Check that all slices are empty
	if len(refs.Agencies) != 0 {
		t.Errorf("Expected Agencies to be empty, got length %d", len(refs.Agencies))
	}
	if len(refs.Routes) != 0 {
		t.Errorf("Expected Routes to be empty, got length %d", len(refs.Routes))
	}
	if len(refs.Situations) != 0 {
		t.Errorf("Expected Situations to be empty, got length %d", len(refs.Situations))
	}
	if len(refs.StopTimes) != 0 {
		t.Errorf("Expected StopTimes to be empty, got length %d", len(refs.StopTimes))
	}
	if len(refs.Stops) != 0 {
		t.Errorf("Expected Stops to be empty, got length %d", len(refs.Stops))
	}
	if len(refs.Trips) != 0 {
		t.Errorf("Expected Trips to be empty, got length %d", len(refs.Trips))
	}
}

func TestReferencesModelJSON(t *testing.T) {
	refs := NewEmptyReferences()
	refs.Agencies = append(refs.Agencies, AgencyReference{ID: "agency1"})
	refs.Routes = append(refs.Routes, Route{ID: "route1"})

	// Marshal to JSON
	jsonData, err := json.Marshal(refs)
	if err != nil {
		t.Fatalf("Failed to marshal ReferencesModel to JSON: %v", err)
	}

	// Unmarshal back to a new struct
	var unmarshaledRefs ReferencesModel
	err = json.Unmarshal(jsonData, &unmarshaledRefs)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to ReferencesModel: %v", err)
	}
	agency := unmarshaledRefs.Agencies[0]
	if agency.ID != "agency1" {
		t.Errorf("Expected agency id 'agency1', got %v", agency.ID)
	}
}
