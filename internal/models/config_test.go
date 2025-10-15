package models

import (
	"encoding/json"
	"testing"
)

func TestGitProperties(t *testing.T) {
	// Create a sample GitProperties
	gitProperties := GitProperties{
		GitBranch:    "hotfix-2.5.13.2",
		GitBuildHost: "ip-10-9-8-24.ec2.internal", GitBuildTime: "20.06.2025 @ 23:06:37 UTC", GitBuildUserEmail: "", GitBuildUserName: "", GitBuildVersion: "2.5.13.3-cs", GitClosestTagCommitCount: "2", GitClosestTagName: "onebusaway-application-modules-2.5.13.2-cs", GitCommitId: "62c50bc48bf2245919458c585f6e3a7b1df074de", GitCommitIdAbbrev: "62c50bc", GitCommitIdDescribe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", GitCommitIdDescribeShort: "onebusaway-application-modules-2.5.13.2-cs-2", GitCommitMessageFull: "changing version for release 2.5.13.3-cs", GitCommitMessageShort: "changing version for release 2.5.13.3-cs", GitCommitTime: "20.06.2025 @ 22:38:35 UTC", GitCommitUserEmail: "CaySavitzky@gmail.com", GitCommitUserName: "CaylaSavitzky", GitDirty: "true", GitRemoteOriginUrl: "https://github.com/camsys/onebusaway-application-modules.git", GitTags: "",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(gitProperties)
	if err != nil {
		t.Fatalf("Failed to marshal GitProperties to JSON: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledModel GitProperties
	err = json.Unmarshal(jsonData, &unmarshaledModel)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to GitProperties: %v", err)
	}

	// Verify fields were preserved correctly
	if unmarshaledModel.GitBranch != gitProperties.GitBranch {
		t.Errorf("Expected GitBranch %s, got %s",
			gitProperties.GitBranch, unmarshaledModel.GitBranch)
	}
	if unmarshaledModel.GitBuildHost != gitProperties.GitBuildHost {
		t.Errorf("Expected GitBuildHost %s, got %s",
			gitProperties.GitBuildHost, unmarshaledModel.GitBuildHost)
	}
	if unmarshaledModel.GitBuildTime != gitProperties.GitBuildTime {
		t.Errorf("Expected GitBuildTime %s, got %s",
			gitProperties.GitBuildTime, unmarshaledModel.GitBuildTime)
	}
	if unmarshaledModel.GitBuildUserEmail != gitProperties.GitBuildUserEmail {
		t.Errorf("Expected GitBuildUserEmail %s, got %s",
			gitProperties.GitBuildUserEmail, unmarshaledModel.GitBuildUserEmail)
	}
	if unmarshaledModel.GitBuildUserName != gitProperties.GitBuildUserName {
		t.Errorf("Expected GitBuildUserName %s, got %s",
			gitProperties.GitBuildUserName, unmarshaledModel.GitBuildUserName)
	}
	if unmarshaledModel.GitBuildVersion != gitProperties.GitBuildVersion {
		t.Errorf("Expected GitBuildVersion %s, got %s",
			gitProperties.GitBuildVersion, unmarshaledModel.GitBuildVersion)
	}
	if unmarshaledModel.GitClosestTagCommitCount != gitProperties.GitClosestTagCommitCount {
		t.Errorf("Expected GitClosestTagCommitCount %s, got %s",
			gitProperties.GitClosestTagCommitCount, unmarshaledModel.GitClosestTagCommitCount)
	}
	if unmarshaledModel.GitClosestTagName != gitProperties.GitClosestTagName {
		t.Errorf("Expected GitClosestTagName %s, got %s",
			gitProperties.GitClosestTagName, unmarshaledModel.GitClosestTagName)
	}
	if unmarshaledModel.GitCommitId != gitProperties.GitCommitId {
		t.Errorf("Expected GitCommitId %s, got %s",
			gitProperties.GitCommitId, unmarshaledModel.GitCommitId)
	}
	if unmarshaledModel.GitCommitIdAbbrev != gitProperties.GitCommitIdAbbrev {
		t.Errorf("Expected GitCommitIdAbbrev %s, got %s",
			gitProperties.GitCommitIdAbbrev, unmarshaledModel.GitCommitIdAbbrev)
	}
	if unmarshaledModel.GitCommitIdDescribe != gitProperties.GitCommitIdDescribe {
		t.Errorf("Expected GitCommitIdDescribe %s, got %s",
			gitProperties.GitCommitIdDescribe, unmarshaledModel.GitCommitIdDescribe)
	}
	if unmarshaledModel.GitCommitIdDescribeShort != gitProperties.GitCommitIdDescribeShort {
		t.Errorf("Expected GitCommitIdDescribeShort %s, got %s",
			gitProperties.GitCommitIdDescribeShort, unmarshaledModel.GitCommitIdDescribeShort)
	}
	if unmarshaledModel.GitCommitMessageFull != gitProperties.GitCommitMessageFull {
		t.Errorf("Expected GitCommitMessageFull %s, got %s",
			gitProperties.GitCommitMessageFull, unmarshaledModel.GitCommitMessageFull)
	}
	if unmarshaledModel.GitCommitMessageShort != gitProperties.GitCommitMessageShort {
		t.Errorf("Expected GitCommitMessageShort %s, got %s",
			gitProperties.GitCommitMessageShort, unmarshaledModel.GitCommitMessageShort)
	}
	if unmarshaledModel.GitCommitTime != gitProperties.GitCommitTime {
		t.Errorf("Expected GitCommitTime %s, got %s",
			gitProperties.GitCommitTime, unmarshaledModel.GitCommitTime)
	}
	if unmarshaledModel.GitCommitUserEmail != gitProperties.GitCommitUserEmail {
		t.Errorf("Expected GitCommitUserEmail %s, got %s",
			gitProperties.GitCommitUserEmail, unmarshaledModel.GitCommitUserEmail)
	}
	if unmarshaledModel.GitCommitUserName != gitProperties.GitCommitUserName {
		t.Errorf("Expected GitCommitUserName %s, got %s",
			gitProperties.GitCommitUserName, unmarshaledModel.GitCommitUserName)
	}
	if unmarshaledModel.GitDirty != gitProperties.GitDirty {
		t.Errorf("Expected GitDirty %s, got %s",
			gitProperties.GitDirty, unmarshaledModel.GitDirty)
	}
	if unmarshaledModel.GitRemoteOriginUrl != gitProperties.GitRemoteOriginUrl {
		t.Errorf("Expected GitRemoteOriginUrl %s, got %s",
			gitProperties.GitRemoteOriginUrl, unmarshaledModel.GitRemoteOriginUrl)
	}
	if unmarshaledModel.GitTags != gitProperties.GitTags {
		t.Errorf("Expected GitTags %s, got %s",
			gitProperties.GitTags, unmarshaledModel.GitTags)
	}
}

func TestConfigModel(t *testing.T) {
	gitProperties := GitProperties{
		GitBranch:    "hotfix-2.5.13.2",
		GitBuildHost: "ip-10-9-8-24.ec2.internal", GitBuildTime: "20.06.2025 @ 23:06:37 UTC", GitBuildUserEmail: "", GitBuildUserName: "", GitBuildVersion: "2.5.13.3-cs", GitClosestTagCommitCount: "2", GitClosestTagName: "onebusaway-application-modules-2.5.13.2-cs", GitCommitId: "62c50bc48bf2245919458c585f6e3a7b1df074de", GitCommitIdAbbrev: "62c50bc", GitCommitIdDescribe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", GitCommitIdDescribeShort: "onebusaway-application-modules-2.5.13.2-cs-2", GitCommitMessageFull: "changing version for release 2.5.13.3-cs", GitCommitMessageShort: "changing version for release 2.5.13.3-cs", GitCommitTime: "20.06.2025 @ 22:38:35 UTC", GitCommitUserEmail: "CaySavitzky@gmail.com", GitCommitUserName: "CaylaSavitzky", GitDirty: "true", GitRemoteOriginUrl: "https://github.com/camsys/onebusaway-application-modules.git", GitTags: "",
	}
	// Create a sample ConfigModel
	configModel := ConfigModel{
		GitProperties:   gitProperties,
		Id:              "id",
		Name:            "name",
		ServiceDateFrom: "serviceDateFrom",
		ServiceDateTo:   "serviceDateTo",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(configModel)
	if err != nil {
		t.Fatalf("Failed to marshal ConfigModel to JSON: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledModel ConfigModel
	err = json.Unmarshal(jsonData, &unmarshaledModel)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to ConfigModel: %v", err)
	}

	// Verify fields were preserved correctly
	if unmarshaledModel.GitProperties != configModel.GitProperties {
		t.Errorf("Expected GitProperties %s, got %s",
			configModel.GitProperties, unmarshaledModel.GitProperties)
	}
	if unmarshaledModel.Id != configModel.Id {
		t.Errorf("Expected Id %s, got %s",
			configModel.Id, unmarshaledModel.Id)
	}
	if unmarshaledModel.Name != configModel.Name {
		t.Errorf("Expected Name %s, got %s",
			configModel.Name, unmarshaledModel.Name)
	}
	if unmarshaledModel.ServiceDateFrom != configModel.ServiceDateFrom {
		t.Errorf("Expected ServiceDateFrom %s, got %s",
			configModel.ServiceDateFrom, unmarshaledModel.ServiceDateFrom)
	}
	if unmarshaledModel.ServiceDateTo != configModel.ServiceDateTo {
		t.Errorf("Expected ServiceDateTo %s, got %s",
			configModel.ServiceDateTo, unmarshaledModel.ServiceDateTo)
	}

}
func TestConfigData(t *testing.T) {

	gitProperties := GitProperties{
		GitBranch:    "hotfix-2.5.13.2",
		GitBuildHost: "ip-10-9-8-24.ec2.internal", GitBuildTime: "20.06.2025 @ 23:06:37 UTC", GitBuildUserEmail: "", GitBuildUserName: "", GitBuildVersion: "2.5.13.3-cs", GitClosestTagCommitCount: "2", GitClosestTagName: "onebusaway-application-modules-2.5.13.2-cs", GitCommitId: "62c50bc48bf2245919458c585f6e3a7b1df074de", GitCommitIdAbbrev: "62c50bc", GitCommitIdDescribe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", GitCommitIdDescribeShort: "onebusaway-application-modules-2.5.13.2-cs-2", GitCommitMessageFull: "changing version for release 2.5.13.3-cs", GitCommitMessageShort: "changing version for release 2.5.13.3-cs", GitCommitTime: "20.06.2025 @ 22:38:35 UTC", GitCommitUserEmail: "CaySavitzky@gmail.com", GitCommitUserName: "CaylaSavitzky", GitDirty: "true", GitRemoteOriginUrl: "https://github.com/camsys/onebusaway-application-modules.git", GitTags: "",
	}
	configModel := ConfigModel{
		GitProperties:   gitProperties,
		Id:              "id",
		Name:            "name",
		ServiceDateFrom: "serviceDateFrom",
		ServiceDateTo:   "serviceDateTo",
	}
	// Create a sample ConfigData

	references := NewEmptyReferences()

	configData := ConfigData{
		Entry:      configModel,
		References: references,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(configData)
	if err != nil {
		t.Fatalf("Failed to marshal configData to JSON: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledData ConfigData
	err = json.Unmarshal(jsonData, &unmarshaledData)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON to ConfigData: %v", err)
	}

	// Verify fields were preserved correctly
	if unmarshaledData.Entry.GitProperties != configModel.GitProperties {
		t.Errorf("Expected GitProperties %s, got %s",
			configModel.GitProperties, unmarshaledData.Entry.GitProperties)
	}
	if unmarshaledData.Entry.Id != configModel.Id {
		t.Errorf("Expected Id %s, got %s",
			configModel.Id, unmarshaledData.Entry.Id)
	}
	if unmarshaledData.Entry.Name != configModel.Name {
		t.Errorf("Expected Name %s, got %s",
			configModel.Name, unmarshaledData.Entry.Name)
	}
	if unmarshaledData.Entry.ServiceDateFrom != configModel.ServiceDateFrom {
		t.Errorf("Expected ServiceDateFrom %s, got %s",
			configModel.ServiceDateFrom, unmarshaledData.Entry.ServiceDateFrom)
	}
	if unmarshaledData.Entry.ServiceDateTo != configModel.ServiceDateTo {
		t.Errorf("Expected ServiceDateTo %s, got %s",
			configModel.ServiceDateTo, unmarshaledData.Entry.ServiceDateTo)
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

func TestNewConfigData(t *testing.T) {
	// This function does not really do much except make a struct and return it
	gitproperties := GitProperties{
		GitBranch:    "hotfix-2.5.13.2",
		GitBuildHost: "ip-10-9-8-24.ec2.internal", GitBuildTime: "20.06.2025 @ 23:06:37 UTC", GitBuildUserEmail: "", GitBuildUserName: "", GitBuildVersion: "2.5.13.3-cs", GitClosestTagCommitCount: "2", GitClosestTagName: "onebusaway-application-modules-2.5.13.2-cs", GitCommitId: "62c50bc48bf2245919458c585f6e3a7b1df074de", GitCommitIdAbbrev: "62c50bc", GitCommitIdDescribe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", GitCommitIdDescribeShort: "onebusaway-application-modules-2.5.13.2-cs-2", GitCommitMessageFull: "changing version for release 2.5.13.3-cs", GitCommitMessageShort: "changing version for release 2.5.13.3-cs", GitCommitTime: "20.06.2025 @ 22:38:35 UTC", GitCommitUserEmail: "CaySavitzky@gmail.com", GitCommitUserName: "CaylaSavitzky", GitDirty: "true", GitRemoteOriginUrl: "https://github.com/camsys/onebusaway-application-modules.git", GitTags: "",
	}
	testCases := []struct {
		testName        string
		gitProperties   GitProperties
		id              string
		name            string
		serviceDateFrom string
		serviceDateTo   string
	}{
		{
			testName:        "One",
			gitProperties:   gitproperties,
			id:              "id",
			name:            "name",
			serviceDateFrom: "serviceDateFrom",
			serviceDateTo:   "serviceDateTo",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			// Call the function being tested
			result := NewConfigData(tc.gitProperties, tc.id, tc.name, tc.serviceDateFrom, tc.serviceDateTo)

			// Verify the fields

			if result.Entry.GitProperties != tc.gitProperties {
				t.Errorf("GitProperties mismatch")
			}
			if result.Entry.Name != tc.name {
				t.Errorf("Expected time %s, got %s", tc.name, result.Entry.Name)
			}
			if result.Entry.Id != tc.id {
				t.Errorf("Expected time %s, got %s", tc.id, result.Entry.Id)
			}
			if result.Entry.ServiceDateFrom != tc.serviceDateFrom {
				t.Errorf("Expected time %s, got %s", tc.serviceDateFrom, result.Entry.ServiceDateFrom)
			}
			if result.Entry.ServiceDateTo != tc.serviceDateTo {
				t.Errorf("Expected time %s, got %s", tc.serviceDateTo, result.Entry.ServiceDateTo)
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

func TestConfigDataEndToEnd(t *testing.T) {
	// Create a gitProperties
	gitproperties := GitProperties{
		GitBranch:    "hotfix-2.5.13.2",
		GitBuildHost: "ip-10-9-8-24.ec2.internal", GitBuildTime: "20.06.2025 @ 23:06:37 UTC", GitBuildUserEmail: "", GitBuildUserName: "", GitBuildVersion: "2.5.13.3-cs", GitClosestTagCommitCount: "2", GitClosestTagName: "onebusaway-application-modules-2.5.13.2-cs", GitCommitId: "62c50bc48bf2245919458c585f6e3a7b1df074de", GitCommitIdAbbrev: "62c50bc", GitCommitIdDescribe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", GitCommitIdDescribeShort: "onebusaway-application-modules-2.5.13.2-cs-2", GitCommitMessageFull: "changing version for release 2.5.13.3-cs", GitCommitMessageShort: "changing version for release 2.5.13.3-cs", GitCommitTime: "20.06.2025 @ 22:38:35 UTC", GitCommitUserEmail: "CaySavitzky@gmail.com", GitCommitUserName: "CaylaSavitzky", GitDirty: "true", GitRemoteOriginUrl: "https://github.com/camsys/onebusaway-application-modules.git", GitTags: "",
	}

	// Create the data using our function
	configData := NewConfigData(gitproperties, "id", "name", "serviceDateFrom", "serviceDateTo")

	// Create a response using this data
	response := NewResponse(200, configData, "OK")

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
	GitProperties, ok := entry["gitProperties"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected entry to be an object, got %T", data["gitProperties"])
	}

	if gitBranch, ok := GitProperties["git.branch"].(string); !ok {
		t.Errorf("Expected GitBranch to be a string, got %T", GitProperties["git.branch"])
	} else {
		expectedGitBranch := gitproperties.GitBranch
		if gitBranch != expectedGitBranch {
			t.Errorf("Expected gitBranch %s, got %s", expectedGitBranch, gitBranch)
		}
	}
	if gitBuildHost, ok := GitProperties["git.build.host"].(string); !ok {
		t.Errorf("Expected GitBuildHost to be a string, got %T", GitProperties["git.build.host"])
	} else {
		expectedGitBuildHost := gitproperties.GitBuildHost
		if gitBuildHost != expectedGitBuildHost {
			t.Errorf("Expected gitBuildHost %s, got %s", expectedGitBuildHost, gitBuildHost)
		}
	}
	if gitBuildTime, ok := GitProperties["git.build.time"].(string); !ok {
		t.Errorf("Expected GitBuildTime to be a string, got %T", GitProperties["git.build.time"])
	} else {
		expectedGitBuildTime := gitproperties.GitBuildTime
		if gitBuildTime != expectedGitBuildTime {
			t.Errorf("Expected gitBuildTime %s, got %s", expectedGitBuildTime, gitBuildTime)
		}
	}
	if gitBuildUserEmail, ok := GitProperties["git.build.user.email"].(string); !ok {
		t.Errorf("Expected GitBuildUserEmail to be a string, got %T", GitProperties["git.build.user.email"])
	} else {
		expectedGitBuildUserEmail := gitproperties.GitBuildUserEmail
		if gitBuildUserEmail != expectedGitBuildUserEmail {
			t.Errorf("Expected gitBuildUserEmail %s, got %s", expectedGitBuildUserEmail, gitBuildUserEmail)
		}
	}
	if gitBuildUserName, ok := GitProperties["git.build.user.name"].(string); !ok {
		t.Errorf("Expected GitBuildUserName to be a string, got %T", GitProperties["git.build.user.name"])
	} else {
		expectedGitUserName := gitproperties.GitBuildUserName
		if gitBuildUserName != expectedGitUserName {
			t.Errorf("Expected BuildUserName %s, got %s", expectedGitUserName, gitBuildUserName)
		}
	}
	if gitBuildVersion, ok := GitProperties["git.build.version"].(string); !ok {
		t.Errorf("Expected GitBuildVersion to be a string, got %T", GitProperties["git.build.version"])
	} else {
		expectedGitBuildVersion := gitproperties.GitBuildVersion
		if gitBuildVersion != expectedGitBuildVersion {
			t.Errorf("Expected BuildVersion %s, got %s", expectedGitBuildVersion, gitBuildVersion)
		}
	}
	if gitClosestTagCommitCount, ok := GitProperties["git.closest.tag.commit.count"].(string); !ok {
		t.Errorf("Expected GitClosestTagCommitCount to be a string, got %T", GitProperties["git.closest.tag.commit.count"])
	} else {
		expectedGitClosestTagCommitCount := gitproperties.GitClosestTagCommitCount
		if gitClosestTagCommitCount != expectedGitClosestTagCommitCount {
			t.Errorf("Expected ClosestTagCommitCount %s, got %s", expectedGitClosestTagCommitCount, gitClosestTagCommitCount)
		}
	}
	if gitClosestTagName, ok := GitProperties["git.closest.tag.name"].(string); !ok {
		t.Errorf("Expected GitClosestTagName to be a string, got %T", GitProperties["git.closest.tag.name"])
	} else {
		expectedGitClosestTagName := gitproperties.GitClosestTagName
		if gitClosestTagName != expectedGitClosestTagName {
			t.Errorf("Expected ClosestTagName %s, got %s", expectedGitClosestTagName, gitClosestTagName)
		}
	}
	if gitCommitId, ok := GitProperties["git.commit.id"].(string); !ok {
		t.Errorf("Expected GitCommitId to be a string, got %T", GitProperties["git.commit.id"])
	} else {
		expectedGitCommitId := gitproperties.GitCommitId
		if gitCommitId != expectedGitCommitId {
			t.Errorf("Expected CommitId %s, got %s", expectedGitCommitId, gitCommitId)
		}
	}
	if gitCommitIdAbbrev, ok := GitProperties["git.commit.id.abbrev"].(string); !ok {
		t.Errorf("Expected GitCommitIdAbbrev to be a string, got %T", GitProperties["git.commit.id.abbrev"])
	} else {
		expectedGitCommitIdAbbrev := gitproperties.GitCommitIdAbbrev
		if gitCommitIdAbbrev != expectedGitCommitIdAbbrev {
			t.Errorf("Expected CommitIdAbbrev %s, got %s", expectedGitCommitIdAbbrev, gitCommitIdAbbrev)
		}
	}
	if gitCommitIdDescribe, ok := GitProperties["git.commit.id.describe"].(string); !ok {
		t.Errorf("Expected GitCommitIdDescribe to be a string, got %T", GitProperties["git.commit.id.describe"])
	} else {
		expectedGitCommitIdDescribe := gitproperties.GitCommitIdDescribe
		if gitCommitIdDescribe != expectedGitCommitIdDescribe {
			t.Errorf("Expected CommitIdDescribe %s, got %s", expectedGitCommitIdDescribe, gitCommitIdDescribe)
		}
	}
	if gitCommitIdDescribeShort, ok := GitProperties["git.commit.id.describe.short"].(string); !ok {
		t.Errorf("Expected GitCommitIdDescribeShort to be a string, got %T", GitProperties["git.commit.id.describe.short"])
	} else {
		expectedGitCommitIdDescribeShort := gitproperties.GitCommitIdDescribeShort
		if gitCommitIdDescribeShort != expectedGitCommitIdDescribeShort {
			t.Errorf("Expected CommitIdDescribeShort %s, got %s", expectedGitCommitIdDescribeShort, gitCommitIdDescribeShort)
		}
	}
	if gitCommitMessageFull, ok := GitProperties["git.commit.message.full"].(string); !ok {
		t.Errorf("Expected GitCommitMessageFull to be a string, got %T", GitProperties["git.commit.message.full"])
	} else {
		expectedGitCommitMessageFull := gitproperties.GitCommitMessageFull
		if gitCommitMessageFull != expectedGitCommitMessageFull {
			t.Errorf("Expected CommitMessageFull %s, got %s", expectedGitCommitMessageFull, gitCommitMessageFull)
		}
	}
	if gitCommitMessageShort, ok := GitProperties["git.commit.message.short"].(string); !ok {
		t.Errorf("Expected GitCommitMessageShort to be a string, got %T", GitProperties["git.commit.message.short"])
	} else {
		expectedGitCommitMessageShort := gitproperties.GitCommitMessageShort
		if gitCommitMessageShort != expectedGitCommitMessageShort {
			t.Errorf("Expected CommitMessageShort %s, got %s", expectedGitCommitMessageShort, gitCommitMessageShort)
		}
	}
	if gitCommitTime, ok := GitProperties["git.commit.time"].(string); !ok {
		t.Errorf("Expected GitCommitTime to be a string, got %T", GitProperties["git.commit.time"])
	} else {
		expectedGitCommitTime := gitproperties.GitCommitTime
		if gitCommitTime != expectedGitCommitTime {
			t.Errorf("Expected CommitTime %s, got %s", expectedGitCommitTime, gitCommitTime)
		}
	}
	if gitCommitUserEmail, ok := GitProperties["git.commit.user.email"].(string); !ok {
		t.Errorf("Expected GitCommitUserEmail to be a string, got %T", GitProperties["git.commit.user.email"])
	} else {
		expectedGitCommitUserEmail := gitproperties.GitCommitUserEmail
		if gitCommitUserEmail != expectedGitCommitUserEmail {
			t.Errorf("Expected CommitUserEmail %s, got %s", expectedGitCommitUserEmail, gitCommitUserEmail)
		}
	}
	if gitCommitUserName, ok := GitProperties["git.commit.user.name"].(string); !ok {
		t.Errorf("Expected GitCommitUserName to be a string, got %T", GitProperties["git.commit.user.name"])
	} else {
		expectedGitCommitUserName := gitproperties.GitCommitUserName
		if gitCommitUserName != expectedGitCommitUserName {
			t.Errorf("Expected CommitUserName %s, got %s", expectedGitCommitUserName, gitCommitUserName)
		}
	}
	if gitDirty, ok := GitProperties["git.dirty"].(string); !ok {
		t.Errorf("Expected GitDirty to be a string, got %T", GitProperties["git.dirty"])
	} else {
		expectedGitDirty := gitproperties.GitDirty
		if gitDirty != expectedGitDirty {
			t.Errorf("Expected Dirty %s, got %s", expectedGitDirty, gitDirty)
		}
	}
	if gitRemoteOriginUrl, ok := GitProperties["git.remote.origin.url"].(string); !ok {
		t.Errorf("Expected GitRemoteOriginUrl to be a string, got %T", GitProperties["git.remote.origin.url"])
	} else {
		expectedGitRemoteOriginUrl := gitproperties.GitRemoteOriginUrl
		if gitRemoteOriginUrl != expectedGitRemoteOriginUrl {
			t.Errorf("Expected RemoteOriginUrl %s, got %s", expectedGitRemoteOriginUrl, gitRemoteOriginUrl)
		}
	}
	if gitTags, ok := GitProperties["git.tags"].(string); !ok {
		t.Errorf("Expected GitTags to be a string, got %T", GitProperties["git.tags"])
	} else {
		expectedGitTags := gitproperties.GitTags
		if gitTags != expectedGitTags {
			t.Errorf("Expected Tags %s, got %s", expectedGitTags, gitTags)
		}
	}

	if id, ok := entry["id"].(string); !ok {
		t.Errorf("Expected Id to be a string, got %T", entry["Id"])
	} else {
		expectedId := "id"
		if id != expectedId {
			t.Errorf("Expected id %s, got %s", expectedId, id)
		}
	}
	if name, ok := entry["name"].(string); !ok {
		t.Errorf("Expected Name to be a string, got %T", entry["Name"])
	} else {
		expectedName := "name"
		if name != expectedName {
			t.Errorf("Expected name %s, got %s", expectedName, name)
		}
	}
	if serviceDateFrom, ok := entry["serviceDateFrom"].(string); !ok {
		t.Errorf("Expected ServiceDateFrom to be a string, got %T", entry["ServiceDateFrom"])
	} else {
		expectedServiceDateFrom := "serviceDateFrom"
		if serviceDateFrom != expectedServiceDateFrom {
			t.Errorf("Expected serviceDateFrom %s, got %s", expectedServiceDateFrom, serviceDateFrom)
		}
	}
	if serviceDateTo, ok := entry["serviceDateTo"].(string); !ok {
		t.Errorf("Expected ServiceDateTo to be a string, got %T", entry["ServiceDateTo"])
	} else {
		expectedServiceDateTo := "serviceDateTo"
		if serviceDateTo != expectedServiceDateTo {
			t.Errorf("Expected serviceDateTo %s, got %s", expectedServiceDateTo, serviceDateTo)
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
