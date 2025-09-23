package models

import (
	"encoding/json"
	"testing"
)

func TestGitProperties(t *testing.T) {
	// Create a sample GitProperties
	gitProperties := GitProperties{
		Git_branch:     "hotfix-2.5.13.2",
		Git_build_host: "ip-10-9-8-24.ec2.internal", Git_build_time: "20.06.2025 @ 23:06:37 UTC", Git_build_user_email: "", Git_build_user_name: "", Git_build_version: "2.5.13.3-cs", Git_closest_tag_commit_count: "2", Git_closest_tag_name: "onebusaway-application-modules-2.5.13.2-cs", Git_commit_id: "62c50bc48bf2245919458c585f6e3a7b1df074de", Git_commit_id_abbrev: "62c50bc", Git_commit_id_describe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", Git_commit_id_describe_short: "onebusaway-application-modules-2.5.13.2-cs-2", Git_commit_message_full: "changing version for release 2.5.13.3-cs", Git_commit_message_short: "changing version for release 2.5.13.3-cs", Git_commit_time: "20.06.2025 @ 22:38:35 UTC", Git_commit_user_email: "CaySavitzky@gmail.com", Git_commit_user_name: "CaylaSavitzky", Git_dirty: "true", Git_remote_origin_url: "https://github.com/camsys/onebusaway-application-modules.git", Git_tags: "",
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
	if unmarshaledModel.Git_branch != gitProperties.Git_branch {
		t.Errorf("Expected Git_branch %s, got %s",
			gitProperties.Git_branch, unmarshaledModel.Git_branch)
	}
	if unmarshaledModel.Git_build_host != gitProperties.Git_build_host {
		t.Errorf("Expected Git_build_host %s, got %s",
			gitProperties.Git_build_host, unmarshaledModel.Git_build_host)
	}
	if unmarshaledModel.Git_build_time != gitProperties.Git_build_time {
		t.Errorf("Expected Git_build_time %s, got %s",
			gitProperties.Git_build_time, unmarshaledModel.Git_build_time)
	}
	if unmarshaledModel.Git_build_user_email != gitProperties.Git_build_user_email {
		t.Errorf("Expected Git_build_user_email %s, got %s",
			gitProperties.Git_build_user_email, unmarshaledModel.Git_build_user_email)
	}
	if unmarshaledModel.Git_build_user_name != gitProperties.Git_build_user_name {
		t.Errorf("Expected Git_build_user_name %s, got %s",
			gitProperties.Git_build_user_name, unmarshaledModel.Git_build_user_name)
	}
	if unmarshaledModel.Git_build_version != gitProperties.Git_build_version {
		t.Errorf("Expected Git_build_version %s, got %s",
			gitProperties.Git_build_version, unmarshaledModel.Git_build_version)
	}
	if unmarshaledModel.Git_closest_tag_commit_count != gitProperties.Git_closest_tag_commit_count {
		t.Errorf("Expected Git_closest_tag_commit_count %s, got %s",
			gitProperties.Git_closest_tag_commit_count, unmarshaledModel.Git_closest_tag_commit_count)
	}
	if unmarshaledModel.Git_closest_tag_name != gitProperties.Git_closest_tag_name {
		t.Errorf("Expected Git_closest_tag_name %s, got %s",
			gitProperties.Git_closest_tag_name, unmarshaledModel.Git_closest_tag_name)
	}
	if unmarshaledModel.Git_commit_id != gitProperties.Git_commit_id {
		t.Errorf("Expected Git_commit_id %s, got %s",
			gitProperties.Git_commit_id, unmarshaledModel.Git_commit_id)
	}
	if unmarshaledModel.Git_commit_id_abbrev != gitProperties.Git_commit_id_abbrev {
		t.Errorf("Expected Git_commit_id_abbrev %s, got %s",
			gitProperties.Git_commit_id_abbrev, unmarshaledModel.Git_commit_id_abbrev)
	}
	if unmarshaledModel.Git_commit_id_describe != gitProperties.Git_commit_id_describe {
		t.Errorf("Expected Git_commit_id_describe %s, got %s",
			gitProperties.Git_commit_id_describe, unmarshaledModel.Git_commit_id_describe)
	}
	if unmarshaledModel.Git_commit_id_describe_short != gitProperties.Git_commit_id_describe_short {
		t.Errorf("Expected Git_commit_id_describe_short %s, got %s",
			gitProperties.Git_commit_id_describe_short, unmarshaledModel.Git_commit_id_describe_short)
	}
	if unmarshaledModel.Git_commit_message_full != gitProperties.Git_commit_message_full {
		t.Errorf("Expected Git_commit_message_full %s, got %s",
			gitProperties.Git_commit_message_full, unmarshaledModel.Git_commit_message_full)
	}
	if unmarshaledModel.Git_commit_message_short != gitProperties.Git_commit_message_short {
		t.Errorf("Expected Git_commit_message_short %s, got %s",
			gitProperties.Git_commit_message_short, unmarshaledModel.Git_commit_message_short)
	}
	if unmarshaledModel.Git_commit_time != gitProperties.Git_commit_time {
		t.Errorf("Expected Git_commit_time %s, got %s",
			gitProperties.Git_commit_time, unmarshaledModel.Git_commit_time)
	}
	if unmarshaledModel.Git_commit_user_email != gitProperties.Git_commit_user_email {
		t.Errorf("Expected Git_commit_user_email %s, got %s",
			gitProperties.Git_commit_user_email, unmarshaledModel.Git_commit_user_email)
	}
	if unmarshaledModel.Git_commit_user_name != gitProperties.Git_commit_user_name {
		t.Errorf("Expected Git_commit_user_name %s, got %s",
			gitProperties.Git_commit_user_name, unmarshaledModel.Git_commit_user_name)
	}
	if unmarshaledModel.Git_dirty != gitProperties.Git_dirty {
		t.Errorf("Expected Git_dirty %s, got %s",
			gitProperties.Git_dirty, unmarshaledModel.Git_dirty)
	}
	if unmarshaledModel.Git_remote_origin_url != gitProperties.Git_remote_origin_url {
		t.Errorf("Expected Git_remote_origin_url %s, got %s",
			gitProperties.Git_remote_origin_url, unmarshaledModel.Git_remote_origin_url)
	}
	if unmarshaledModel.Git_tags != gitProperties.Git_tags {
		t.Errorf("Expected Git_tags %s, got %s",
			gitProperties.Git_tags, unmarshaledModel.Git_tags)
	}
}

func TestConfigModel(t *testing.T) {
	gitProperties := GitProperties{
		Git_branch:     "hotfix-2.5.13.2",
		Git_build_host: "ip-10-9-8-24.ec2.internal", Git_build_time: "20.06.2025 @ 23:06:37 UTC", Git_build_user_email: "", Git_build_user_name: "", Git_build_version: "2.5.13.3-cs", Git_closest_tag_commit_count: "2", Git_closest_tag_name: "onebusaway-application-modules-2.5.13.2-cs", Git_commit_id: "62c50bc48bf2245919458c585f6e3a7b1df074de", Git_commit_id_abbrev: "62c50bc", Git_commit_id_describe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", Git_commit_id_describe_short: "onebusaway-application-modules-2.5.13.2-cs-2", Git_commit_message_full: "changing version for release 2.5.13.3-cs", Git_commit_message_short: "changing version for release 2.5.13.3-cs", Git_commit_time: "20.06.2025 @ 22:38:35 UTC", Git_commit_user_email: "CaySavitzky@gmail.com", Git_commit_user_name: "CaylaSavitzky", Git_dirty: "true", Git_remote_origin_url: "https://github.com/camsys/onebusaway-application-modules.git", Git_tags: "",
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
		Git_branch:     "hotfix-2.5.13.2",
		Git_build_host: "ip-10-9-8-24.ec2.internal", Git_build_time: "20.06.2025 @ 23:06:37 UTC", Git_build_user_email: "", Git_build_user_name: "", Git_build_version: "2.5.13.3-cs", Git_closest_tag_commit_count: "2", Git_closest_tag_name: "onebusaway-application-modules-2.5.13.2-cs", Git_commit_id: "62c50bc48bf2245919458c585f6e3a7b1df074de", Git_commit_id_abbrev: "62c50bc", Git_commit_id_describe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", Git_commit_id_describe_short: "onebusaway-application-modules-2.5.13.2-cs-2", Git_commit_message_full: "changing version for release 2.5.13.3-cs", Git_commit_message_short: "changing version for release 2.5.13.3-cs", Git_commit_time: "20.06.2025 @ 22:38:35 UTC", Git_commit_user_email: "CaySavitzky@gmail.com", Git_commit_user_name: "CaylaSavitzky", Git_dirty: "true", Git_remote_origin_url: "https://github.com/camsys/onebusaway-application-modules.git", Git_tags: "",
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
		Git_branch:     "hotfix-2.5.13.2",
		Git_build_host: "ip-10-9-8-24.ec2.internal", Git_build_time: "20.06.2025 @ 23:06:37 UTC", Git_build_user_email: "", Git_build_user_name: "", Git_build_version: "2.5.13.3-cs", Git_closest_tag_commit_count: "2", Git_closest_tag_name: "onebusaway-application-modules-2.5.13.2-cs", Git_commit_id: "62c50bc48bf2245919458c585f6e3a7b1df074de", Git_commit_id_abbrev: "62c50bc", Git_commit_id_describe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", Git_commit_id_describe_short: "onebusaway-application-modules-2.5.13.2-cs-2", Git_commit_message_full: "changing version for release 2.5.13.3-cs", Git_commit_message_short: "changing version for release 2.5.13.3-cs", Git_commit_time: "20.06.2025 @ 22:38:35 UTC", Git_commit_user_email: "CaySavitzky@gmail.com", Git_commit_user_name: "CaylaSavitzky", Git_dirty: "true", Git_remote_origin_url: "https://github.com/camsys/onebusaway-application-modules.git", Git_tags: "",
	}
	testCases := []struct {
		test_name       string
		gitProperties   GitProperties
		id              string
		name            string
		serviceDateFrom string
		serviceDateTo   string
	}{
		{
			test_name:       "One",
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
		Git_branch:     "hotfix-2.5.13.2",
		Git_build_host: "ip-10-9-8-24.ec2.internal", Git_build_time: "20.06.2025 @ 23:06:37 UTC", Git_build_user_email: "", Git_build_user_name: "", Git_build_version: "2.5.13.3-cs", Git_closest_tag_commit_count: "2", Git_closest_tag_name: "onebusaway-application-modules-2.5.13.2-cs", Git_commit_id: "62c50bc48bf2245919458c585f6e3a7b1df074de", Git_commit_id_abbrev: "62c50bc", Git_commit_id_describe: "onebusaway-application-modules-2.5.13.2-cs-2-g62c50bc", Git_commit_id_describe_short: "onebusaway-application-modules-2.5.13.2-cs-2", Git_commit_message_full: "changing version for release 2.5.13.3-cs", Git_commit_message_short: "changing version for release 2.5.13.3-cs", Git_commit_time: "20.06.2025 @ 22:38:35 UTC", Git_commit_user_email: "CaySavitzky@gmail.com", Git_commit_user_name: "CaylaSavitzky", Git_dirty: "true", Git_remote_origin_url: "https://github.com/camsys/onebusaway-application-modules.git", Git_tags: "",
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

	if git_branch, ok := GitProperties["git.branch"].(string); !ok {
		t.Errorf("Expected Git_branch to be a string, got %T", GitProperties["git.branch"])
	} else {
		expectedGitBranch := gitproperties.Git_branch
		if git_branch != expectedGitBranch {
			t.Errorf("Expected gitBranch %s, got %s", expectedGitBranch, git_branch)
		}
	}
	if git_build_host, ok := GitProperties["git.build.host"].(string); !ok {
		t.Errorf("Expected Git_build_host to be a string, got %T", GitProperties["git.build.host"])
	} else {
		expectedGitBuildHost := gitproperties.Git_build_host
		if git_build_host != expectedGitBuildHost {
			t.Errorf("Expected gitBuildHost %s, got %s", expectedGitBuildHost, git_build_host)
		}
	}
	if git_build_time, ok := GitProperties["git.build.time"].(string); !ok {
		t.Errorf("Expected Git_build_time to be a string, got %T", GitProperties["git.build.time"])
	} else {
		expectedGitBuildTime := gitproperties.Git_build_time
		if git_build_time != expectedGitBuildTime {
			t.Errorf("Expected gitBuildTime %s, got %s", expectedGitBuildTime, git_build_time)
		}
	}
	if git_build_user_email, ok := GitProperties["git.build.user.email"].(string); !ok {
		t.Errorf("Expected Git_build_user_email to be a string, got %T", GitProperties["git.build.user.email"])
	} else {
		expectedGitBuildUserEmail := gitproperties.Git_build_user_email
		if git_build_user_email != expectedGitBuildUserEmail {
			t.Errorf("Expected gitBuildUserEmail %s, got %s", expectedGitBuildUserEmail, git_build_user_email)
		}
	}
	if git_build_user_name, ok := GitProperties["git.build.user.name"].(string); !ok {
		t.Errorf("Expected Git_build_user_name to be a string, got %T", GitProperties["git.build.user.name"])
	} else {
		expectedGitUserName := gitproperties.Git_build_user_name
		if git_build_user_name != expectedGitUserName {
			t.Errorf("Expected BuildUserName %s, got %s", expectedGitUserName, git_build_user_name)
		}
	}
	if git_build_version, ok := GitProperties["git.build.version"].(string); !ok {
		t.Errorf("Expected Git_build_version to be a string, got %T", GitProperties["git.build.version"])
	} else {
		expectedGitBuildVersion := gitproperties.Git_build_version
		if git_build_version != expectedGitBuildVersion {
			t.Errorf("Expected BuildVersion %s, got %s", expectedGitBuildVersion, git_build_version)
		}
	}
	if git_closest_tag_commit_count, ok := GitProperties["git.closest.tag.commit.count"].(string); !ok {
		t.Errorf("Expected Git_closest_tag_commit_count to be a string, got %T", GitProperties["git.closest.tag.commit.count"])
	} else {
		expectedGitClosestTagCommitCount := gitproperties.Git_closest_tag_commit_count
		if git_closest_tag_commit_count != expectedGitClosestTagCommitCount {
			t.Errorf("Expected ClosestTagCommitCount %s, got %s", expectedGitClosestTagCommitCount, git_closest_tag_commit_count)
		}
	}
	if git_closest_tag_name, ok := GitProperties["git.closest.tag.name"].(string); !ok {
		t.Errorf("Expected Git_closest_tag_name to be a string, got %T", GitProperties["git.closest.tag.name"])
	} else {
		expectedGitClosestTagName := gitproperties.Git_closest_tag_name
		if git_closest_tag_name != expectedGitClosestTagName {
			t.Errorf("Expected ClosestTagName %s, got %s", expectedGitClosestTagName, git_closest_tag_name)
		}
	}
	if git_commit_id, ok := GitProperties["git.commit.id"].(string); !ok {
		t.Errorf("Expected Git_commit_id to be a string, got %T", GitProperties["git.commit.id"])
	} else {
		expectedGitCommitId := gitproperties.Git_commit_id
		if git_commit_id != expectedGitCommitId {
			t.Errorf("Expected CommitId %s, got %s", expectedGitCommitId, git_commit_id)
		}
	}
	if git_commit_id_abbrev, ok := GitProperties["git.commit.id.abbrev"].(string); !ok {
		t.Errorf("Expected Git_commit_id_abbrev to be a string, got %T", GitProperties["git.commit.id.abbrev"])
	} else {
		expectedGitCommitIdAbbrev := gitproperties.Git_commit_id_abbrev
		if git_commit_id_abbrev != expectedGitCommitIdAbbrev {
			t.Errorf("Expected CommitIdAbbrev %s, got %s", expectedGitCommitIdAbbrev, git_commit_id_abbrev)
		}
	}
	if git_commit_id_describe, ok := GitProperties["git.commit.id.describe"].(string); !ok {
		t.Errorf("Expected Git_commit_id_describe to be a string, got %T", GitProperties["git.commit.id.describe"])
	} else {
		expectedGitCommitIdDescribe := gitproperties.Git_commit_id_describe
		if git_commit_id_describe != expectedGitCommitIdDescribe {
			t.Errorf("Expected CommitIdDescribe %s, got %s", expectedGitCommitIdDescribe, git_commit_id_describe)
		}
	}
	if git_commit_id_describe_short, ok := GitProperties["git.commit.id.describe.short"].(string); !ok {
		t.Errorf("Expected Git_commit_id_describe_short to be a string, got %T", GitProperties["git.commit.id.describe.short"])
	} else {
		expectedGitCommitIdDescribeShort := gitproperties.Git_commit_id_describe_short
		if git_commit_id_describe_short != expectedGitCommitIdDescribeShort {
			t.Errorf("Expected CommitIdDescribeShort %s, got %s", expectedGitCommitIdDescribeShort, git_commit_id_describe_short)
		}
	}
	if git_commit_message_full, ok := GitProperties["git.commit.message.full"].(string); !ok {
		t.Errorf("Expected Git_commit_message_full to be a string, got %T", GitProperties["git.commit.message.full"])
	} else {
		expectedGitCommitMessageFull := gitproperties.Git_commit_message_full
		if git_commit_message_full != expectedGitCommitMessageFull {
			t.Errorf("Expected CommitMessageFull %s, got %s", expectedGitCommitMessageFull, git_commit_message_full)
		}
	}
	if git_commit_message_short, ok := GitProperties["git.commit.message.short"].(string); !ok {
		t.Errorf("Expected Git_commit_message_short to be a string, got %T", GitProperties["git.commit.message.short"])
	} else {
		expectedGitCommitMessageShort := gitproperties.Git_commit_message_short
		if git_commit_message_short != expectedGitCommitMessageShort {
			t.Errorf("Expected CommitMessageShort %s, got %s", expectedGitCommitMessageShort, git_commit_message_short)
		}
	}
	if git_commit_time, ok := GitProperties["git.commit.time"].(string); !ok {
		t.Errorf("Expected Git_commit_time to be a string, got %T", GitProperties["git.commit.time"])
	} else {
		expectedGitCommitTime := gitproperties.Git_commit_time
		if git_commit_time != expectedGitCommitTime {
			t.Errorf("Expected CommitTime %s, got %s", expectedGitCommitTime, git_commit_time)
		}
	}
	if git_commit_user_email, ok := GitProperties["git.commit.user.email"].(string); !ok {
		t.Errorf("Expected Git_commit_user_email to be a string, got %T", GitProperties["git.commit.user.email"])
	} else {
		expectedGitCommitUserEmail := gitproperties.Git_commit_user_email
		if git_commit_user_email != expectedGitCommitUserEmail {
			t.Errorf("Expected CommitUserEmail %s, got %s", expectedGitCommitUserEmail, git_commit_user_email)
		}
	}
	if git_commit_user_name, ok := GitProperties["git.commit.user.name"].(string); !ok {
		t.Errorf("Expected Git_commit_user_name to be a string, got %T", GitProperties["git.commit.user.name"])
	} else {
		expectedGitCommitUserName := gitproperties.Git_commit_user_name
		if git_commit_user_name != expectedGitCommitUserName {
			t.Errorf("Expected CommitUserName %s, got %s", expectedGitCommitUserName, git_commit_user_name)
		}
	}
	if git_dirty, ok := GitProperties["git.dirty"].(string); !ok {
		t.Errorf("Expected Git_dirty to be a string, got %T", GitProperties["git.dirty"])
	} else {
		expectedGitDirty := gitproperties.Git_dirty
		if git_dirty != expectedGitDirty {
			t.Errorf("Expected Dirty %s, got %s", expectedGitDirty, git_dirty)
		}
	}
	if git_remote_origin_url, ok := GitProperties["git.remote.origin.url"].(string); !ok {
		t.Errorf("Expected Git_remote_origin_url to be a string, got %T", GitProperties["git.remote.origin.url"])
	} else {
		expectedGitRemoteOriginUrl := gitproperties.Git_remote_origin_url
		if git_remote_origin_url != expectedGitRemoteOriginUrl {
			t.Errorf("Expected RemoteOriginUrl %s, got %s", expectedGitRemoteOriginUrl, git_remote_origin_url)
		}
	}
	if git_tags, ok := GitProperties["git.tags"].(string); !ok {
		t.Errorf("Expected Git_tags to be a string, got %T", GitProperties["git.tags"])
	} else {
		expectedGitTags := gitproperties.Git_tags
		if git_tags != expectedGitTags {
			t.Errorf("Expected Tags %s, got %s", expectedGitTags, git_tags)
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
