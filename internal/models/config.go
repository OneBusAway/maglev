package models
//GitProperties uses to store all git.properties
type GitProperties struct {
	Git_branch                   string `json:"git.branch"`
	Git_build_host               string `json:"git.build.host"`
	Git_build_time               string `json:"git.build.time"`
	Git_build_user_email         string `json:"git.build.user.email"`
	Git_build_user_name          string `json:"git.build.user.name"`
	Git_build_version            string `json:"git.build.version"`
	Git_closest_tag_commit_count string `json:"git.closest.tag.commit.count"`
	Git_closest_tag_name         string `json:"git.closest.tag.name"`
	Git_commit_id                string `json:"git.commit.id"`
	Git_commit_id_abbrev         string `json:"git.commit.id.abbrev"`
	Git_commit_id_describe       string `json:"git.commit.id.describe"`
	Git_commit_id_describe_short string `json:"git.commit.id.describe.short"`
	Git_commit_message_full      string `json:"git.commit.message.full"`
	Git_commit_message_short     string `json:"git.commit.message.short"`
	Git_commit_time              string `json:"git.commit.time"`
	Git_commit_user_email        string `json:"git.commit.user.email"`
	Git_commit_user_name         string `json:"git.commit.user.name"`
	Git_dirty                    string `json:"git.dirty"`
	Git_remote_origin_url        string `json:"git.remote.origin.url"`
	Git_tags                     string `json:"git.tags"`
}

// ConfigModel Config specific model
type ConfigModel struct {
	GitProperties   GitProperties `json:"gitProperties"`
	Id              string        `json:"id"`
	Name            string        `json:"name"`
	ServiceDateFrom string        `json:"serviceDateFrom"`
	ServiceDateTo   string        `json:"serviceDateTo"`
}

// ConfigData Combined data structure for config endpoint
type ConfigData struct {
	Entry      ConfigModel     `json:"entry"`
	References ReferencesModel `json:"references"`
}

// NewGitProperties creates GitProperties structure based on provided inputs
func NewGitProperties(Git_branch string, Git_build_host string, Git_build_time string, Git_build_user_email string, Git_build_user_name string, Git_build_version string, Git_closest_tag_commit_count string, Git_closest_tag_name string, Git_commit_id string, Git_commit_id_abbrev, Git_commit_id_describe string, Git_commit_id_describe_short string, Git_commit_message_full string, Git_commit_message_short string, Git_commit_time string, Git_commit_user_email string, Git_commit_user_name string, Git_dirty string, Git_remote_origin_url string, Git_tags string) GitProperties {
	return GitProperties{
		Git_branch:                   Git_branch,
		Git_build_host:               Git_build_host,
		Git_build_time:               Git_build_time,
		Git_build_user_email:         Git_build_user_email,
		Git_build_user_name:          Git_build_user_name,
		Git_build_version:            Git_build_version,
		Git_closest_tag_commit_count: Git_closest_tag_commit_count,
		Git_closest_tag_name:         Git_closest_tag_name,
		Git_commit_id:                Git_commit_id,
		Git_commit_id_abbrev:         Git_commit_id_abbrev,
		Git_commit_id_describe:       Git_commit_id_describe,
		Git_commit_id_describe_short: Git_commit_id_describe_short,
		Git_commit_message_full:      Git_commit_message_full,
		Git_commit_message_short:     Git_commit_message_short,
		Git_commit_time:              Git_commit_time,
		Git_commit_user_email:        Git_commit_user_email,
		Git_commit_user_name:         Git_commit_user_name,
		Git_dirty:                    Git_dirty,
		Git_remote_origin_url:        Git_remote_origin_url,
		Git_tags:                     Git_tags,
	}
}

// NewConfigData creates a ConfigData structure based on provided inputs
func NewConfigData(gitProperties GitProperties, id string, name string, serviceDateFrom string, serviceDateTo string) ConfigData {
	return ConfigData{
		Entry: ConfigModel{
			GitProperties:   gitProperties,
			Id:              id,
			Name:            name,
			ServiceDateFrom: serviceDateFrom,
			ServiceDateTo:   serviceDateTo,
		},
		References: NewEmptyReferences(),
	}
}
