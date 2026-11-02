package restapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/internal/buildinfo"
	"maglev.onebusaway.org/internal/models"
)

// configHandler returns server configuration metadata, including the Git commit hash and build info.
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

	var serviceDateFrom, serviceDateTo string
	if api.GtfsManager != nil && api.GtfsManager.GtfsDB != nil && api.GtfsManager.GtfsDB.Queries != nil {
		metadata, err := api.GtfsManager.GtfsDB.Queries.GetImportMetadata(r.Context())
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.ErrorContext(r.Context(), "failed to read import metadata for config service date range", "error", err)
			}
		} else if metadata.ImportTime != 0 {
			importTime := time.Unix(0, metadata.ImportTime).UTC()
			serviceDateFrom = strconv.FormatInt(importTime.UnixMilli(), 10)
			serviceDateTo = strconv.FormatInt(importTime.Add(30*24*time.Hour).UnixMilli(), 10)
		}
	}

	configEntry := models.ConfigModel{
		GitProperties:   gitProps,
		Id:              "oba-maglev",
		Name:            "OneBusAway Go",
		ServiceDateFrom: serviceDateFrom,
		ServiceDateTo:   serviceDateTo,
	}

	response := models.NewEntryResponse(
		configEntry,
		*models.NewEmptyReferences(),
		api.Clock,
	)

	api.sendResponse(w, r, response)
}
