package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

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

	// Codes of a rejected new job. The settings page shows a message for each.
	errCodeInvalidInput      = "invalid_input"
	errCodeChannelNotFound   = "channel_not_found"
	errCodeChannelArchived   = "channel_archived"
	errCodeRobinNotInChannel = "robin_not_in_channel"
	errCodeUserNotInChannel  = "user_not_in_channel"
	errCodeJobLimitReached   = "job_limit_reached"
)

type jobRunResponse struct {
	Status      string     `json:"status"`
	Failure     string     `json:"failure"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	Deadline    *time.Time `json:"deadline"`
	FinishedAt  *time.Time `json:"finished_at"`
}

type jobResponse struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	ChannelID   string          `json:"channel_id"`
	ChannelName string          `json:"channel_name"`
	Hour        int             `json:"hour"`
	Minute      int             `json:"minute"`
	TimeZone    string          `json:"time_zone"`
	NextRunAt   time.Time       `json:"next_run_at"`
	LastRun     *jobRunResponse `json:"last_run"`
}

type jobsResponse struct {
	Available bool          `json:"available"`
	MaxJobs   int           `json:"max_jobs"`
	Jobs      []jobResponse `json:"jobs"`
}

type jobCreateRequest struct {
	Kind      string `json:"kind"`
	ChannelID string `json:"channel_id"`
	Hour      int    `json:"hour"`
	Minute    int    `json:"minute"`
	TimeZone  string `json:"time_zone"`
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

func toJobResponse(j *model.Job) jobResponse {
	out := jobResponse{
		ID:          string(j.ID),
		Kind:        string(j.Kind),
		ChannelID:   j.ChannelID,
		ChannelName: j.ChannelName,
		Hour:        j.Schedule.Hour,
		Minute:      j.Schedule.Minute,
		TimeZone:    j.Schedule.TimeZone,
		NextRunAt:   j.NextRunAt.UTC(),
	}
	if r := j.LastRun; r != nil {
		out.LastRun = &jobRunResponse{
			Status:      string(r.Status),
			Failure:     string(r.Failure),
			ScheduledAt: r.ScheduledAt.UTC(),
			Deadline:    optionalTime(r.Deadline),
			FinishedAt:  optionalTime(r.FinishedAt),
		}
	}
	return out
}

func (s *Server) jobsListHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}
	if s.jobUC == nil {
		writeJSON(ctx, w, http.StatusOK, jobsResponse{Jobs: []jobResponse{}})
		return
	}

	list, err := s.jobUC.List(ctx, session.Key())
	if err != nil {
		errutil.Handle(ctx, err, "failed to list jobs")
		writeError(ctx, w, http.StatusInternalServerError, errCodeInternal)
		return
	}
	jobs := make([]jobResponse, 0, len(list.Jobs))
	for _, j := range list.Jobs {
		jobs = append(jobs, toJobResponse(j))
	}
	writeJSON(ctx, w, http.StatusOK, jobsResponse{Available: true, MaxJobs: list.MaxJobs, Jobs: jobs})
}

// rejectedJobs maps the errors of a new job that come from the user's input
// to the status and code the settings page reads.
var rejectedJobs = []struct {
	err    error
	status int
	code   string
}{
	{usecase.ErrJobInvalidInput, http.StatusBadRequest, errCodeInvalidInput},
	{usecase.ErrJobChannelNotFound, http.StatusBadRequest, errCodeChannelNotFound},
	{usecase.ErrJobChannelArchived, http.StatusBadRequest, errCodeChannelArchived},
	{usecase.ErrJobRobinNotInChannel, http.StatusBadRequest, errCodeRobinNotInChannel},
	{usecase.ErrJobUserNotInChannel, http.StatusBadRequest, errCodeUserNotInChannel},
	{usecase.ErrJobLimitReached, http.StatusConflict, errCodeJobLimitReached},
}

func (s *Server) jobsCreateHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}

	var req jobCreateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, jobRequestMaxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "invalid job request", goerr.T(errutil.TagBenign)), "job was not added")
		writeError(ctx, w, http.StatusBadRequest, errCodeInvalidInput)
		return
	}

	job, err := s.jobUC.Create(ctx, session.Key(), usecase.JobInput{
		Kind:      model.JobKind(req.Kind),
		ChannelID: req.ChannelID,
		Hour:      req.Hour,
		Minute:    req.Minute,
		TimeZone:  req.TimeZone,
	})
	if err != nil {
		for _, rej := range rejectedJobs {
			if errors.Is(err, rej.err) {
				errutil.Handle(ctx, goerr.Wrap(err, "job rejected", goerr.T(errutil.TagBenign)), "job was not added")
				writeError(ctx, w, rej.status, rej.code)
				return
			}
		}
		errutil.Handle(ctx, err, "failed to add job")
		writeError(ctx, w, http.StatusInternalServerError, errCodeInternal)
		return
	}
	writeJSON(ctx, w, http.StatusCreated, toJobResponse(job))
}

func (s *Server) jobsDeleteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session, ok := sessionFromRequest(w, r)
	if !ok {
		return
	}
	id := model.JobID(chi.URLParam(r, "jobID"))
	if err := id.Validate(); err != nil {
		writeError(ctx, w, http.StatusNotFound, errCodeNotFound)
		return
	}
	if err := s.jobUC.Delete(ctx, session.Key(), id); err != nil {
		errutil.Handle(ctx, err, "failed to delete job")
		writeError(ctx, w, http.StatusInternalServerError, errCodeInternal)
		return
	}
	writeJSON(ctx, w, http.StatusOK, successResponse{Success: true})
}
