package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	httpctrl "github.com/m-mizutani/robin/pkg/controller/http"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
)

type jobCreateCall struct {
	Key   model.UserKey
	Input usecase.JobInput
}

type jobDeleteCall struct {
	Key model.UserKey
	ID  model.JobID
}

type fakeJobUseCase struct {
	mu        sync.Mutex
	list      *usecase.JobList
	listErr   error
	listKeys  []model.UserKey
	created   *model.Job
	createErr error
	creates   []jobCreateCall
	deleteErr error
	deletes   []jobDeleteCall
}

func (f *fakeJobUseCase) List(_ context.Context, key model.UserKey) (*usecase.JobList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listKeys = append(f.listKeys, key)
	return f.list, f.listErr
}

func (f *fakeJobUseCase) Create(_ context.Context, key model.UserKey, in usecase.JobInput) (*model.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, jobCreateCall{Key: key, Input: in})
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.created, nil
}

func (f *fakeJobUseCase) Delete(_ context.Context, key model.UserKey, id model.JobID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, jobDeleteCall{Key: key, ID: id})
	return f.deleteErr
}

const (
	jobsBase  = "/api/v1/jobs"
	testJobID = model.JobID("00000000-0000-4000-8000-000000000001")
)

func testJob() *model.Job {
	created := time.Date(2026, 10, 4, 23, 30, 0, 0, time.UTC)
	return &model.Job{
		TeamID:      sessionKey.TeamID,
		UserID:      sessionKey.UserID,
		ID:          testJobID,
		Kind:        model.JobKindHello,
		ChannelID:   "C0GENERAL",
		ChannelName: "general",
		Schedule:    model.DailySchedule{Hour: 9, Minute: 5, TimeZone: "Asia/Tokyo"},
		NextRunAt:   time.Date(2026, 10, 5, 0, 5, 0, 0, time.UTC),
		CreatedAt:   created,
		UpdatedAt:   created,
	}
}

func testJobJSON(lastRun any) map[string]any {
	return map[string]any{
		"id": string(testJobID), "kind": "hello", "channel_id": "C0GENERAL", "channel_name": "general",
		"hour": float64(9), "minute": float64(5), "time_zone": "Asia/Tokyo",
		"next_run_at": "2026-10-05T00:05:00Z", "last_run": lastRun,
	}
}

func newJobTestServer(t *testing.T, authUC *fakeAuthUseCase, jobUC *fakeJobUseCase) *httpctrl.Server {
	t.Helper()
	var opts []httpctrl.Option
	if jobUC != nil {
		opts = append(opts, httpctrl.WithJobs(jobUC))
	}
	srv, err := httpctrl.New(authUC, httpctrl.Config{BaseURL: "https://robin.example.com", Static: testStatic}, opts...)
	gt.NoError(t, err).Required()
	return srv
}

func TestJobsList(t *testing.T) {
	t.Run("feature not configured", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, nil)
		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"available": false, "max_jobs": float64(0), "jobs": []any{}})
	})

	t.Run("jobs of the user", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		withRun := testJob()
		withRun.LastRun = &model.JobRunSummary{
			RunID:       "20261004T000500Z",
			Status:      model.JobRunFailed,
			Failure:     model.JobRunTimedOut,
			ScheduledAt: time.Date(2026, 10, 4, 0, 5, 0, 0, time.UTC),
			Deadline:    time.Date(2026, 10, 4, 0, 8, 0, 0, time.UTC),
			FinishedAt:  time.Date(2026, 10, 4, 0, 8, 1, 0, time.UTC),
		}
		jobUC := &fakeJobUseCase{list: &usecase.JobList{Jobs: []*model.Job{testJob(), withRun}, MaxJobs: 10}}
		srv := newJobTestServer(t, authUC, jobUC)

		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{
			"available": true, "max_jobs": float64(10),
			"jobs": []any{
				testJobJSON(nil),
				testJobJSON(map[string]any{
					"status": "failed", "failure": "timed_out", "scheduled_at": "2026-10-04T00:05:00Z",
					"deadline": "2026-10-04T00:08:00Z", "finished_at": "2026-10-04T00:08:01Z",
				}),
			},
		})
		gt.Value(t, jobUC.listKeys).Equal([]model.UserKey{sessionKey})
	})

	t.Run("skipped run has no deadline", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		job := testJob()
		job.LastRun = &model.JobRunSummary{
			RunID: "20261004T000500Z", Status: model.JobRunSkipped,
			ScheduledAt: time.Date(2026, 10, 4, 0, 5, 0, 0, time.UTC),
			FinishedAt:  time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC),
		}
		srv := newJobTestServer(t, authUC, &fakeJobUseCase{list: &usecase.JobList{Jobs: []*model.Job{job}, MaxJobs: 10}})
		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		body := decodeJSON(t, resp.Body)
		run := body["jobs"].([]any)[0].(map[string]any)["last_run"].(map[string]any)
		gt.Value(t, run["deadline"]).Nil()
		gt.Value(t, run["finished_at"]).Equal("2026-10-04T02:00:00Z")
	})

	t.Run("without a session", func(t *testing.T) {
		srv := newJobTestServer(t, newFakeAuthUseCase(), &fakeJobUseCase{})
		resp := serve(srv, httptest.NewRequest(http.MethodGet, jobsBase, nil))
		gt.Number(t, resp.StatusCode).Equal(http.StatusUnauthorized)
	})

	t.Run("usecase failure", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, &fakeJobUseCase{listErr: errors.New("firestore down")})
		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusInternalServerError)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": "internal_error"})
	})
}

func postJob(srv *httpctrl.Server, authUC *fakeAuthUseCase, body string) *http.Response {
	r := httptest.NewRequest(http.MethodPost, jobsBase, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return serve(srv, withSession(r, authUC))
}

const validJobBody = `{"kind":"hello","channel_id":"C0GENERAL","hour":9,"minute":5,"time_zone":"Asia/Tokyo"}`

func TestJobsCreate(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{created: testJob()}
		srv := newJobTestServer(t, authUC, jobUC)

		resp := postJob(srv, authUC, validJobBody)
		gt.Number(t, resp.StatusCode).Equal(http.StatusCreated)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(testJobJSON(nil))
		gt.Value(t, jobUC.creates).Equal([]jobCreateCall{{Key: sessionKey, Input: usecase.JobInput{
			Kind: model.JobKindHello, ChannelID: "C0GENERAL", Hour: 9, Minute: 5, TimeZone: "Asia/Tokyo",
		}}})
	})

	t.Run("malformed request", func(t *testing.T) {
		for name, body := range map[string]string{
			"not JSON":    `kind=hello`,
			"unknown key": `{"kind":"hello","channel_id":"C0GENERAL","hour":9,"minute":5,"time_zone":"Asia/Tokyo","owner":"U0BOB"}`,
			"too large":   `{"kind":"` + strings.Repeat("a", 5000) + `"}`,
			"wrong type":  `{"kind":"hello","channel_id":"C0GENERAL","hour":"9","minute":5,"time_zone":"Asia/Tokyo"}`,
		} {
			t.Run(name, func(t *testing.T) {
				authUC := newFakeAuthUseCase()
				jobUC := &fakeJobUseCase{created: testJob()}
				srv := newJobTestServer(t, authUC, jobUC)
				resp := postJob(srv, authUC, body)
				gt.Number(t, resp.StatusCode).Equal(http.StatusBadRequest)
				gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": "invalid_input"})
				gt.A(t, jobUC.creates).Length(0)
			})
		}
	})

	t.Run("rejected by the usecase", func(t *testing.T) {
		cases := map[string]struct {
			err    error
			status int
			code   string
		}{
			"invalid input":         {usecase.ErrJobInvalidInput, http.StatusBadRequest, "invalid_input"},
			"channel not found":     {usecase.ErrJobChannelNotFound, http.StatusBadRequest, "channel_not_found"},
			"channel archived":      {usecase.ErrJobChannelArchived, http.StatusBadRequest, "channel_archived"},
			"robin not in channel":  {usecase.ErrJobRobinNotInChannel, http.StatusBadRequest, "robin_not_in_channel"},
			"user not in channel":   {usecase.ErrJobUserNotInChannel, http.StatusBadRequest, "user_not_in_channel"},
			"limit reached":         {usecase.ErrJobLimitReached, http.StatusConflict, "job_limit_reached"},
			"slack or store failed": {errors.New("ratelimited"), http.StatusInternalServerError, "internal_error"},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				authUC := newFakeAuthUseCase()
				srv := newJobTestServer(t, authUC, &fakeJobUseCase{createErr: goerr.Wrap(c.err, "wrapped")})
				resp := postJob(srv, authUC, validJobBody)
				gt.Number(t, resp.StatusCode).Equal(c.status)
				gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": c.code})
			})
		}
	})

	t.Run("without a session", func(t *testing.T) {
		jobUC := &fakeJobUseCase{created: testJob()}
		srv := newJobTestServer(t, newFakeAuthUseCase(), jobUC)
		r := httptest.NewRequest(http.MethodPost, jobsBase, strings.NewReader(validJobBody))
		resp := serve(srv, r)
		gt.Number(t, resp.StatusCode).Equal(http.StatusUnauthorized)
		gt.A(t, jobUC.creates).Length(0)
	})

	t.Run("feature not configured", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, nil)
		resp := postJob(srv, authUC, validJobBody)
		gt.Number(t, resp.StatusCode).Equal(http.StatusNotFound)
	})
}

func TestJobsDelete(t *testing.T) {
	deleteReq := func(id string) *http.Request {
		return httptest.NewRequest(http.MethodDelete, jobsBase+"/"+id, nil)
	}

	t.Run("deleted", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(deleteReq(string(testJobID)), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"success": true})
		gt.Value(t, jobUC.deletes).Equal([]jobDeleteCall{{Key: sessionKey, ID: testJobID}})
	})

	t.Run("malformed ID", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(deleteReq("not-a-job"), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusNotFound)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": "not_found"})
		gt.A(t, jobUC.deletes).Length(0)
	})

	t.Run("usecase failure", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, &fakeJobUseCase{deleteErr: errors.New("firestore down")})
		resp := serve(srv, withSession(deleteReq(string(testJobID)), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusInternalServerError)
	})

	t.Run("without a session", func(t *testing.T) {
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, newFakeAuthUseCase(), jobUC)
		resp := serve(srv, deleteReq(string(testJobID)))
		gt.Number(t, resp.StatusCode).Equal(http.StatusUnauthorized)
		gt.A(t, jobUC.deletes).Length(0)
	})

	t.Run("feature not configured", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, nil)
		resp := serve(srv, withSession(deleteReq(string(testJobID)), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusNotFound)
	})
}
