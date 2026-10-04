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

type saveCall struct {
	Key       model.UserKey
	ChannelID string
	TimeZone  string
}

type addCall struct {
	Key model.UserKey
	Job model.JobName
	At  model.DailyTime
}

type deleteCall struct {
	Key model.UserKey
	ID  model.JobTriggerID
}

type fakeJobUseCase struct {
	mu        sync.Mutex
	setting   *model.JobSetting
	getErr    error
	getKeys   []model.UserKey
	saveErr   error
	saves     []saveCall
	trigger   *model.JobTrigger
	addErr    error
	adds      []addCall
	deleteErr error
	deletes   []deleteCall
}

func (f *fakeJobUseCase) Get(_ context.Context, key model.UserKey) (*model.JobSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getKeys = append(f.getKeys, key)
	return f.setting, f.getErr
}

func (f *fakeJobUseCase) Save(_ context.Context, key model.UserKey, channelID, timeZone string) (*model.JobSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves = append(f.saves, saveCall{key, channelID, timeZone})
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	return &model.JobSetting{TeamID: key.TeamID, UserID: key.UserID, ChannelID: channelID, TimeZone: timeZone}, nil
}

func (f *fakeJobUseCase) AddTrigger(_ context.Context, key model.UserKey, job model.JobName, at model.DailyTime) (*model.JobTrigger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds = append(f.adds, addCall{key, job, at})
	if f.addErr != nil {
		return nil, f.addErr
	}
	return f.trigger, nil
}

func (f *fakeJobUseCase) DeleteTrigger(_ context.Context, key model.UserKey, id model.JobTriggerID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, deleteCall{key, id})
	return f.deleteErr
}

const (
	jobsBase      = "/api/v1/jobs"
	testTriggerID = model.JobTriggerID("00000000-0000-4000-8000-000000000001")
)

func testTrigger() model.JobTrigger {
	return model.JobTrigger{
		ID: testTriggerID, Job: model.JobNameHello, Time: model.DailyTime{Hour: 9, Minute: 5},
		NextRunAt: time.Date(2026, 10, 5, 0, 5, 0, 0, time.UTC),
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

func jsonRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestJobsGet(t *testing.T) {
	t.Run("without a setting", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"setting": nil, "triggers": []any{}})
		gt.Value(t, jobUC.getKeys).Equal([]model.UserKey{sessionKey})
	})

	t.Run("with a setting and triggers", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{setting: &model.JobSetting{
			ChannelID: "C0GENERAL", TimeZone: "Asia/Tokyo", Triggers: []model.JobTrigger{testTrigger()},
		}}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{
			"setting":  map[string]any{"channel_id": "C0GENERAL", "time_zone": "Asia/Tokyo"},
			"triggers": []any{map[string]any{"id": string(testTriggerID), "job": "hello", "hour": float64(9), "minute": float64(5)}},
		})
	})

	t.Run("usecase failure", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, &fakeJobUseCase{getErr: errors.New("firestore down")})
		resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusInternalServerError)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": "internal_error"})
	})
}

func TestJobSettingPut(t *testing.T) {
	const body = `{"channel_id":"C0GENERAL","time_zone":"Asia/Tokyo"}`

	t.Run("saved", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(jsonRequest(http.MethodPut, jobsBase+"/setting", body), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"channel_id": "C0GENERAL", "time_zone": "Asia/Tokyo"})
		gt.Value(t, jobUC.saves).Equal([]saveCall{{sessionKey, "C0GENERAL", "Asia/Tokyo"}})
	})

	for name, c := range map[string]struct {
		body   string
		err    error
		status int
		code   string
	}{
		"not JSON":          {body: `channel=C0`, status: http.StatusBadRequest, code: "invalid_input"},
		"unknown key":       {body: `{"channel_id":"C0GENERAL","time_zone":"UTC","user_id":"U0BOB"}`, status: http.StatusBadRequest, code: "invalid_input"},
		"too large":         {body: `{"channel_id":"` + strings.Repeat("C", 5000) + `"}`, status: http.StatusBadRequest, code: "invalid_input"},
		"rejected input":    {body: body, err: usecase.ErrJobInputInvalid, status: http.StatusBadRequest, code: "invalid_input"},
		"store unavailable": {body: body, err: errors.New("firestore down"), status: http.StatusInternalServerError, code: "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			authUC := newFakeAuthUseCase()
			jobUC := &fakeJobUseCase{}
			if c.err != nil {
				jobUC.saveErr = goerr.Wrap(c.err, "wrapped")
			}
			srv := newJobTestServer(t, authUC, jobUC)
			resp := serve(srv, withSession(jsonRequest(http.MethodPut, jobsBase+"/setting", c.body), authUC))
			gt.Number(t, resp.StatusCode).Equal(c.status)
			gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": c.code})
		})
	}
}

func TestJobTriggerPost(t *testing.T) {
	const body = `{"job":"hello","hour":9,"minute":5}`

	t.Run("added", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		tr := testTrigger()
		jobUC := &fakeJobUseCase{trigger: &tr}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(jsonRequest(http.MethodPost, jobsBase+"/triggers", body), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusCreated)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"id": string(testTriggerID), "job": "hello", "hour": float64(9), "minute": float64(5)})
		gt.Value(t, jobUC.adds).Equal([]addCall{{sessionKey, model.JobNameHello, model.DailyTime{Hour: 9, Minute: 5}}})
	})

	t.Run("midnight is a time, not a missing one", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		tr := testTrigger()
		jobUC := &fakeJobUseCase{trigger: &tr}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(jsonRequest(http.MethodPost, jobsBase+"/triggers", `{"job":"hello","hour":0,"minute":0}`), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusCreated)
		gt.Value(t, jobUC.adds).Equal([]addCall{{sessionKey, model.JobNameHello, model.DailyTime{}}})
	})

	for name, body := range map[string]string{
		"no hour":     `{"job":"hello","minute":5}`,
		"null minute": `{"job":"hello","hour":9,"minute":null}`,
		"no time":     `{"job":"hello"}`,
	} {
		t.Run(name, func(t *testing.T) {
			authUC := newFakeAuthUseCase()
			jobUC := &fakeJobUseCase{}
			srv := newJobTestServer(t, authUC, jobUC)
			resp := serve(srv, withSession(jsonRequest(http.MethodPost, jobsBase+"/triggers", body), authUC))
			gt.Number(t, resp.StatusCode).Equal(http.StatusBadRequest)
			gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": "invalid_input"})
			gt.A(t, jobUC.adds).Length(0)
		})
	}

	for name, c := range map[string]struct {
		body   string
		err    error
		status int
		code   string
	}{
		"wrong type":        {body: `{"job":"hello","hour":"9","minute":5}`, status: http.StatusBadRequest, code: "invalid_input"},
		"rejected input":    {body: body, err: usecase.ErrJobInputInvalid, status: http.StatusBadRequest, code: "invalid_input"},
		"no setting":        {body: body, err: usecase.ErrJobSettingRequired, status: http.StatusConflict, code: "setting_required"},
		"store unavailable": {body: body, err: errors.New("firestore down"), status: http.StatusInternalServerError, code: "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			authUC := newFakeAuthUseCase()
			jobUC := &fakeJobUseCase{}
			if c.err != nil {
				jobUC.addErr = goerr.Wrap(c.err, "wrapped")
			}
			srv := newJobTestServer(t, authUC, jobUC)
			resp := serve(srv, withSession(jsonRequest(http.MethodPost, jobsBase+"/triggers", c.body), authUC))
			gt.Number(t, resp.StatusCode).Equal(c.status)
			gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"error": c.code})
		})
	}
}

func TestJobTriggerDelete(t *testing.T) {
	deleteReq := func(id string) *http.Request {
		return httptest.NewRequest(http.MethodDelete, jobsBase+"/triggers/"+id, nil)
	}

	t.Run("deleted", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(deleteReq(string(testTriggerID)), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusOK)
		gt.Value(t, decodeJSON(t, resp.Body)).Equal(map[string]any{"success": true})
		gt.Value(t, jobUC.deletes).Equal([]deleteCall{{sessionKey, testTriggerID}})
	})

	t.Run("malformed ID", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		jobUC := &fakeJobUseCase{}
		srv := newJobTestServer(t, authUC, jobUC)
		resp := serve(srv, withSession(deleteReq("not-a-trigger"), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusNotFound)
		gt.A(t, jobUC.deletes).Length(0)
	})

	t.Run("usecase failure", func(t *testing.T) {
		authUC := newFakeAuthUseCase()
		srv := newJobTestServer(t, authUC, &fakeJobUseCase{deleteErr: errors.New("firestore down")})
		resp := serve(srv, withSession(deleteReq(string(testTriggerID)), authUC))
		gt.Number(t, resp.StatusCode).Equal(http.StatusInternalServerError)
	})
}

func TestJobs_RequireASession(t *testing.T) {
	jobUC := &fakeJobUseCase{}
	srv := newJobTestServer(t, newFakeAuthUseCase(), jobUC)
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, jobsBase, nil),
		jsonRequest(http.MethodPut, jobsBase+"/setting", `{"channel_id":"C0GENERAL","time_zone":"UTC"}`),
		jsonRequest(http.MethodPost, jobsBase+"/triggers", `{"job":"hello","hour":9,"minute":0}`),
		httptest.NewRequest(http.MethodDelete, jobsBase+"/triggers/"+string(testTriggerID), nil),
	} {
		resp := serve(srv, r)
		gt.Number(t, resp.StatusCode).Equal(http.StatusUnauthorized)
	}
	gt.A(t, jobUC.saves).Length(0)
	gt.A(t, jobUC.adds).Length(0)
	gt.A(t, jobUC.deletes).Length(0)
}

func TestJobs_NotMountedWithoutTheUseCase(t *testing.T) {
	authUC := newFakeAuthUseCase()
	srv := newJobTestServer(t, authUC, nil)
	resp := serve(srv, withSession(httptest.NewRequest(http.MethodGet, jobsBase, nil), authUC))
	gt.Number(t, resp.StatusCode).Equal(http.StatusNotFound)
}
