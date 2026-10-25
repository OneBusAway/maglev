package restapi

import (
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
	if api.GtfsManager != nil {
		startDate, endDate, err := api.GtfsManager.FeedServiceDateRange(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to get feed service date range", "error", err)
		} else {
			loc := agencyTimezone(api, r)
			serviceDateFrom = formatServiceDate(startDate, loc)
			serviceDateTo = formatServiceDate(endDate, loc)
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

func formatServiceDate(dateStr string, loc *time.Location) string {
	if dateStr == "" {
		return ""
	}
	t, err := time.ParseInLocation("20060102", dateStr, loc)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
}
