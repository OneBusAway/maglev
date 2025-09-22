package restapi

//generates git_properties.txt at compile time
//go:generate sh -c "../../scripts/git-properties.sh > git/git_properties.txt"

import (
	_ "embed"
	"maglev.onebusaway.org/internal/models"
	"net/http"
	"strings"
)

// embeds git_properties.txt to the binary
//
//go:embed git/git_properties.txt
var gitProperties string

func (api *RestAPI) config(w http.ResponseWriter, r *http.Request) {
	lines := strings.Split(gitProperties, "\n") //splits each property
	git_branch, git_build_host, git_build_time, git_build_user_email, git_build_user_name, git_build_version, git_closest_tag_commit_count, git_closest_tag_name, git_commit_id, git_commit_id_abbrev, git_commit_id_describe, git_commit_id_describe_short, git_commit_message_full, git_commit_message_short, git_commit_time, git_commit_user_email, git_commit_user_name, git_dirty, git_remote_origin_url, git_tags := lines[0], lines[1], lines[2], lines[3], lines[4], lines[5], lines[6], lines[7], lines[8], lines[9], lines[10], lines[11], lines[12], lines[13], lines[14], lines[15], lines[16], lines[17], lines[18], lines[19]
	if git_dirty == "1" { //fixes git.dirty from 1,0 to true,falsegi
		git_dirty = "true"
	} else {
		git_dirty = "false"
	}
	git := models.NewGitProperties(
		git_branch,
		git_build_host,
		git_build_time,
		git_build_user_email,
		git_build_user_name,
		git_build_version,
		git_closest_tag_commit_count,
		git_closest_tag_name,
		git_commit_id,
		git_commit_id_abbrev,
		git_commit_id_describe,
		git_commit_id_describe_short,
		git_commit_message_full,
		git_commit_message_short,
		git_commit_time,
		git_commit_user_email,
		git_commit_user_name,
		git_dirty,
		git_remote_origin_url,
		git_tags,
	)
	configData := models.NewConfigData(
		git,
		"TODO",
		"TODO",
		"TODO",
		"TODO",
	) //TODO : add the 4 things

	response := models.NewOKResponse(configData)

	api.sendResponse(w, r, response)
}
