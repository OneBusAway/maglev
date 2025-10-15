package restapi

//generates git_properties.txt at compile time
//go:generate sh -c "../../scripts/git-properties.sh > git/git_properties.txt"

import (
	_ "embed"
	"net/http"
	"strings"

	"maglev.onebusaway.org/internal/models"
)

// embeds git_properties.txt to the binary
//
//go:embed git/git_properties.txt
var gitProperties string

func (api *RestAPI) config(w http.ResponseWriter, r *http.Request) {
	lines := strings.Split(gitProperties, "\n")
	props := make(map[string]string)
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			props[key] = strings.TrimSpace(value)
		}
	}
	if props["git_dirty"] == "0" { //fixes git_dirty 0->false else true.
		props["git_dirty"] = "false"
	} else {
		props["git_dirty"] = "true"
	}
	git := models.NewGitProperties()
	git.SetGitBranch(props["git_branch"])
	git.SetGitBuildHost(props["git_build_host"])
	git.SetGitBuildTime(props["git_build_time"])
	git.SetGitBuildUserEmail(props["git_build_user_email"])
	git.SetGitBuildUserName(props["git_build_user_name"])
	git.SetGitBuildVersion(props["git_build_version"])
	git.SetGitClosestTagCommitCount(props["git_closest_tag_commit_count"])
	git.SetGitClosestTagName(props["git_closest_tag_name"])
	git.SetGitCommitId(props["git_commit_id"])
	git.SetGitCommitIdAbbrev(props["git_commit_id_abbrev"])
	git.SetGitCommitIdDescribe(props["git_commit_id_describe"])
	git.SetGitCommitIdDescribeShort(props["git_commit_id_describe_short"])
	git.SetGitCommitMessageFull(props["git_commit_message_full"])
	git.SetGitCommitMessageShort(props["git_commit_message_short"])
	git.SetGitCommitTime(props["git_commit_time"])
	git.SetGitCommitUserEmail(props["git_commit_user_email"])
	git.SetGitCommitUserName(props["git_commit_user_name"])
	git.SetGitDirty(props["git_dirty"])
	git.SetGitRemoteOriginUrl(props["git_remote_origin_url"])
	git.SetGitTags(props["git_tags"])

	configData := models.NewConfigData(
		*git,
		"", // id - bundle-specific, empty for now
		"", // name - bundle-specific, empty for now
		"", // serviceDateFrom - will implement later
		"", // serviceDateTo - will implement later
	)

	response := models.NewOKResponse(configData)

	api.sendResponse(w, r, response)
}
