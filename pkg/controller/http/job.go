package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
)

const (
	jobsPath = apiV1Path + "/jobs"

	// jobRequestMaxBytes is far above the size of a valid request.
	jobRequestMaxBytes = 4096

	errCodeInvalidInput    = "invalid_input"
	errCodeSettingRequired = "setting_required"
)

type jobSettingResponse struct {
	ChannelID string `json:"channel_id"`
	TimeZone  string `json:"time_zone"`
}

type jobTriggerResponse struct {
	ID     string `json:"id"`
	Job    string `json:"job"`
	Hour   int    `json:"hour"`
	Minute int    `json:"minute"`
}

type jobsResponse struct {
	Setting  *jobSettingResponse  `json:"setting"`
	Triggers []jobTriggerResponse `json:"triggers"`
}

type jobSettingRequest struct {
	ChannelID string `json:"channel_id"`
	TimeZone  string `json:"time_zone"`
}

// jobTriggerRequest takes pointers so that a missing or null hour or minute
// is rejected instead of becoming 0, which is a valid time.
type jobTriggerRequest struct {
	Job    string `json:"job"`
	Hour   *int   `json:"hour"`
	Minute *int   `json:"minute"`
}

func toJobTriggerResponse(t model.JobTrigger) jobTriggerResponse {
	return jobTriggerResponse{ID: string(t.ID), Job: string(t.Job), Hour: t.Time.Hour, Minute: t.Time.Minute}
}

// decodeJobRequest reads a small JSON body without unknown keys. On failure
// it writes the error response and returns false.
func decodeJobRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, jobRequestMaxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		errutil.Handle(r.Context(), goerr.Wrap(err, "invalid job request", goerr.T(errutil.TagBenign)), "job request rejected")
		writeError(r.Context(), w, http.StatusBadRequest, errCodeInvalidInput)
		return false
	}
	return true
}

// writeJobError answers the errors that come from the user's input with a
// fixed code, and any other error with 500.
func writeJobError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	ctx := r.Context()
	switch {
	case errors.Is(err, usecase.ErrJobInputInvalid):
		errutil.Handle(ctx, goerr.Wrap(err, msg, goerr.T(errutil.TagBenign)), "job request rejected")
		writeError(ctx, w, http.StatusBadRequest, errCodeInvalidInput)
	case errors.Is(err, usecase.ErrJobSettingRequired):
		errutil.Handle(ctx, goerr.Wrap(err, msg, goerr.T(errutil.TagBenign)), "job request rejected")
		writeError(ctx, w, http.StatusConflict, errCodeSettingRequired)
	default:
		errutil.Handle(ctx, err, msg)
		writeError(ctx, w, http.StatusInternalServerError, errCodeInternal)
	}
}

func (s *Server) jobsGetHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}
	setting, err := s.jobUC.Get(ctx, session.Key())
	if err != nil {
		writeJobError(w, r, err, "failed to get job setting")
		return
	}
	resp := jobsResponse{Triggers: []jobTriggerResponse{}}
	if setting != nil {
		resp.Setting = &jobSettingResponse{ChannelID: setting.ChannelID, TimeZone: setting.TimeZone}
		for _, t := range setting.Triggers {
			resp.Triggers = append(resp.Triggers, toJobTriggerResponse(t))
		}
	}
	writeJSON(ctx, w, http.StatusOK, resp)
}

func (s *Server) jobSettingPutHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}
	var req jobSettingRequest
	if !decodeJobRequest(w, r, &req) {
		return
	}
	setting, err := s.jobUC.Save(ctx, session.Key(), req.ChannelID, req.TimeZone)
	if err != nil {
		writeJobError(w, r, err, "failed to save job setting")
		return
	}
	writeJSON(ctx, w, http.StatusOK, jobSettingResponse{ChannelID: setting.ChannelID, TimeZone: setting.TimeZone})
}

func (s *Server) jobTriggerPostHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}
	var req jobTriggerRequest
	if !decodeJobRequest(w, r, &req) {
		return
	}
	if req.Hour == nil || req.Minute == nil {
		errutil.Handle(ctx, goerr.New("job trigger request without a time", goerr.T(errutil.TagBenign),
			goerr.V("has_hour", req.Hour != nil), goerr.V("has_minute", req.Minute != nil)), "job request rejected")
		writeError(ctx, w, http.StatusBadRequest, errCodeInvalidInput)
		return
	}
	t, err := s.jobUC.AddTrigger(ctx, session.Key(), model.JobName(req.Job), model.DailyTime{Hour: *req.Hour, Minute: *req.Minute})
	if err != nil {
		writeJobError(w, r, err, "failed to add job trigger")
		return
	}
	writeJSON(ctx, w, http.StatusCreated, toJobTriggerResponse(*t))
}

func (s *Server) jobTriggerDeleteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}
	id := model.JobTriggerID(chi.URLParam(r, "triggerID"))
	if err := id.Validate(); err != nil {
		writeError(ctx, w, http.StatusNotFound, errCodeNotFound)
		return
	}
	if err := s.jobUC.DeleteTrigger(ctx, session.Key(), id); err != nil {
		writeJobError(w, r, err, "failed to delete job trigger")
		return
	}
	writeJSON(ctx, w, http.StatusOK, successResponse{Success: true})
}
