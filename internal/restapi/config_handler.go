package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/buildinfo"
	"maglev.onebusaway.org/internal/models"
)

func (api *RestAPI) configHandler(w http.ResponseWriter, r *http.Request) {
	shortHash := "unknown"
	if len(buildinfo.CommitHash) >= 7 {
		shortHash = buildinfo.CommitHash[:7]
	}

	gitProps := models.GitProperties{
		GitBranch:                buildinfo.Branch,
		GitBuildTime:             buildinfo.BuildTime,
		GitBuildVersion:          buildinfo.Version,
		GitCommitId:              buildinfo.CommitHash,
		GitCommitTime:            buildinfo.CommitTime,
		GitDirty:                 buildinfo.Dirty,
		GitCommitIdAbbrev:        shortHash,
		GitBuildHost:             buildinfo.Host,
		GitBuildUserEmail:        buildinfo.UserEmail,
		GitBuildUserName:         buildinfo.UserName,
		GitCommitUserEmail:       buildinfo.UserEmail,
		GitCommitUserName:        buildinfo.UserName,
		GitRemoteOriginUrl:       buildinfo.RemoteURL,
		GitCommitMessageShort:    buildinfo.CommitMessage,
		GitCommitMessageFull:     buildinfo.CommitMessage,
		GitCommitIdDescribe:      buildinfo.Version,
		GitCommitIdDescribeShort: buildinfo.Version,
	}

	configEntry := models.ConfigModel{
		GitProperties:   gitProps,
		Id:              "oba-maglev",
		Name:            "OneBusAway Go",
		ServiceDateFrom: "",
		ServiceDateTo:   "",
	}

	response := models.NewEntryResponse(
		configEntry,
		*models.NewEmptyReferences(),
		api.Clock,
	)

	api.sendResponse(w, r, response)
}
