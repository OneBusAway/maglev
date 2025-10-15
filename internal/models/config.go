package models

// GitProperties uses to store all git.properties
type GitProperties struct {
	GitBranch                string `json:"git.branch"`
	GitBuildHost             string `json:"git.build.host"`
	GitBuildTime             string `json:"git.build.time"`
	GitBuildUserEmail        string `json:"git.build.user.email"`
	GitBuildUserName         string `json:"git.build.user.name"`
	GitBuildVersion          string `json:"git.build.version"`
	GitClosestTagCommitCount string `json:"git.closest.tag.commit.count"`
	GitClosestTagName        string `json:"git.closest.tag.name"`
	GitCommitId              string `json:"git.commit.id"`
	GitCommitIdAbbrev        string `json:"git.commit.id.abbrev"`
	GitCommitIdDescribe      string `json:"git.commit.id.describe"`
	GitCommitIdDescribeShort string `json:"git.commit.id.describe.short"`
	GitCommitMessageFull     string `json:"git.commit.message.full"`
	GitCommitMessageShort    string `json:"git.commit.message.short"`
	GitCommitTime            string `json:"git.commit.time"`
	GitCommitUserEmail       string `json:"git.commit.user.email"`
	GitCommitUserName        string `json:"git.commit.user.name"`
	GitDirty                 string `json:"git.dirty"`
	GitRemoteOriginUrl       string `json:"git.remote.origin.url"`
	GitTags                  string `json:"git.tags"`
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
func NewGitProperties() *GitProperties {
	return &GitProperties{}
}

func (git *GitProperties) SetGitBranch(GitBranch string) {
	git.GitBranch = GitBranch
}
func (git *GitProperties) SetGitBuildHost(GitBuildHost string) {
	git.GitBuildHost = GitBuildHost
}
func (git *GitProperties) SetGitBuildTime(GitBuildTime string) {
	git.GitBuildTime = GitBuildTime
}
func (git *GitProperties) SetGitBuildUserEmail(GitBuildUserEmail string) {
	git.GitBuildUserEmail = GitBuildUserEmail
}
func (git *GitProperties) SetGitBuildUserName(GitBuildUserName string) {
	git.GitBuildUserName = GitBuildUserName
}
func (git *GitProperties) SetGitBuildVersion(GitBuildVersion string) {
	git.GitBuildVersion = GitBuildVersion
}
func (git *GitProperties) SetGitClosestTagCommitCount(GitClosestTagCommitCount string) {
	git.GitClosestTagCommitCount = GitClosestTagCommitCount
}
func (git *GitProperties) SetGitClosestTagName(GitClosestTagName string) {
	git.GitClosestTagName = GitClosestTagName
}
func (git *GitProperties) SetGitCommitId(GitCommitId string) {
	git.GitCommitId = GitCommitId
}
func (git *GitProperties) SetGitCommitIdAbbrev(GitCommitIdAbbrev string) {
	git.GitCommitIdAbbrev = GitCommitIdAbbrev
}
func (git *GitProperties) SetGitCommitIdDescribe(GitCommitIdDescribe string) {
	git.GitCommitIdDescribe = GitCommitIdDescribe
}
func (git *GitProperties) SetGitCommitIdDescribeShort(GitCommitIdDescribeShort string) {
	git.GitCommitIdDescribeShort = GitCommitIdDescribeShort
}
func (git *GitProperties) SetGitCommitMessageFull(GitCommitMessageFull string) {
	git.GitCommitMessageFull = GitCommitMessageFull
}
func (git *GitProperties) SetGitCommitMessageShort(GitCommitMessageShort string) {
	git.GitCommitMessageShort = GitCommitMessageShort
}
func (git *GitProperties) SetGitCommitTime(GitCommitTime string) {
	git.GitCommitTime = GitCommitTime
}
func (git *GitProperties) SetGitCommitUserEmail(GitCommitUserEmail string) {
	git.GitCommitUserEmail = GitCommitUserEmail
}
func (git *GitProperties) SetGitCommitUserName(GitCommitUserName string) {
	git.GitCommitUserName = GitCommitUserName
}
func (git *GitProperties) SetGitDirty(GitDirty string) {
	git.GitDirty = GitDirty
}
func (git *GitProperties) SetGitRemoteOriginUrl(GitRemoteOriginUrl string) {
	git.GitRemoteOriginUrl = GitRemoteOriginUrl
}
func (git *GitProperties) SetGitTags(GitTags string) {
	git.GitTags = GitTags
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
