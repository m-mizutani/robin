package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/m-mizutani/gt"
	"github.com/slack-go/slack/slackevents"

	httpctrl "github.com/m-mizutani/robin/pkg/controller/http"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/domain/model/auth"
	"github.com/m-mizutani/robin/pkg/usecase"
)

const testSigningSecret = "signing-secret"

type fakeAuthUseCase struct {
	mu            sync.Mutex
	authorizeURL  string
	states        []string
	callbackCodes []string
	session       *auth.Session
	secret        auth.SessionSecret
	callbackErr   error
	authErr       error
	logoutErr     error
	logouts       []auth.SessionID
	logoutSecrets []auth.SessionSecret
	me            *usecase.Me
	meErr         error
	meKeys        []model.UserKey
}

func newFakeAuthUseCase() *fakeAuthUseCase {
	now := time.Now().UTC().Truncate(time.Second)
	return &fakeAuthUseCase{
		authorizeURL: "https://slack.com/oauth/v2/authorize?client_id=x",
		session: &auth.Session{
			ID:        auth.NewSessionID(),
			TeamID:    "T0123ABCD",
			UserID:    "U0123ABCD",
			CreatedAt: now,
			ExpiresAt: now.Add(7 * 24 * time.Hour),
		},
		secret: auth.NewSessionSecret(),
		me: &usecase.Me{
			TeamID:         "T0123ABCD",
			UserID:         "U0123ABCD",
			Name:           "Alice Example",
			SlackConnected: true,
		},
	}
}

func (f *fakeAuthUseCase) AuthorizeURL(state string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, state)
	return f.authorizeURL + "&state=" + state
}

func (f *fakeAuthUseCase) HandleCallback(_ context.Context, code string) (*auth.Session, auth.SessionSecret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbackCodes = append(f.callbackCodes, code)
	if f.callbackErr != nil {
		return nil, "", f.callbackErr
	}
	return f.session, f.secret, nil
}

func (f *fakeAuthUseCase) Authenticate(_ context.Context, id auth.SessionID, secret auth.SessionSecret) (*auth.Session, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	if id != f.session.ID || secret != f.secret {
		return nil, usecase.ErrUnauthenticated
	}
	return f.session, nil
}

func (f *fakeAuthUseCase) Logout(_ context.Context, id auth.SessionID, secret auth.SessionSecret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logouts = append(f.logouts, id)
	f.logoutSecrets = append(f.logoutSecrets, secret)
	return f.logoutErr
}

func (f *fakeAuthUseCase) Me(_ context.Context, key model.UserKey) (*usecase.Me, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meKeys = append(f.meKeys, key)
	if f.meErr != nil {
		return nil, f.meErr
	}
	return f.me, nil
}

type fakeSlackEventUseCase struct {
	mu        sync.Mutex
	events    []*slackevents.EventsAPIEvent
	shortcuts []model.SlackMessageShortcut
}

func (f *fakeSlackEventUseCase) HandleMessageShortcut(_ context.Context, s model.SlackMessageShortcut) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shortcuts = append(f.shortcuts, s)
	return nil
}

func (f *fakeSlackEventUseCase) handledShortcuts() []model.SlackMessageShortcut {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.SlackMessageShortcut(nil), f.shortcuts...)
}

func (f *fakeSlackEventUseCase) HandleEvent(_ context.Context, event *slackevents.EventsAPIEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	return nil
}

func (f *fakeSlackEventUseCase) handled() []*slackevents.EventsAPIEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*slackevents.EventsAPIEvent(nil), f.events...)
}

var testStatic = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Robin</title>")},
	"assets/app.js": {Data: []byte("console.log('app')")},
}

func newTestServer(t *testing.T, baseURL string, authUC *fakeAuthUseCase, slackUC *fakeSlackEventUseCase) *httpctrl.Server {
	t.Helper()
	srv, err := httpctrl.New(authUC, httpctrl.Config{
		BaseURL: baseURL,
		Static:  testStatic,
	}, httpctrl.WithSlackEvents(slackUC, testSigningSecret))
	gt.NoError(t, err).Required()
	return srv
}

func TestServer_WithoutSlackEvents(t *testing.T) {
	srv, err := httpctrl.New(newFakeAuthUseCase(), httpctrl.Config{BaseURL: "http://localhost:8080", Static: testStatic})
	gt.NoError(t, err).Required()

	for _, path := range []string{"/hooks/slack/event", "/hooks/slack/interaction"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		gt.Number(t, w.Code).NotEqual(http.StatusOK)
		gt.Number(t, w.Code).NotEqual(http.StatusUnauthorized)
	}
}

func TestServer_SlackEventsNeedSigningSecret(t *testing.T) {
	_, err := httpctrl.New(newFakeAuthUseCase(), httpctrl.Config{BaseURL: "http://localhost:8080", Static: testStatic},
		httpctrl.WithSlackEvents(&fakeSlackEventUseCase{}, ""))
	gt.Value(t, err).NotNil()
}

func decodeJSON(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var out map[string]any
	gt.NoError(t, json.NewDecoder(body).Decode(&out)).Required()
	return out
}

func TestServer_SPA(t *testing.T) {
	srv := newTestServer(t, "https://robin.example.com", newFakeAuthUseCase(), &fakeSlackEventUseCase{})

	for _, path := range []string{"/", "/login", "/settings"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			gt.Number(t, w.Code).Equal(http.StatusOK)
			gt.String(t, w.Body.String()).Contains("<title>Robin</title>")
		})
	}

	t.Run("existing asset", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
		gt.Number(t, w.Code).Equal(http.StatusOK)
		gt.String(t, w.Body.String()).Equal("console.log('app')")
	})

	for _, path := range []string{"/api/foo", "/api/v1/foo", "/api/v1/auth/unknown"} {
		t.Run("unknown api "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			gt.Number(t, w.Code).Equal(http.StatusNotFound)
			gt.Value(t, decodeJSON(t, w.Body)["error"]).Equal("not_found")
		})
	}
}

// The API moved under /api/v1 without keeping the old paths.
func TestServer_PathsBeforeV1AreNotFound(t *testing.T) {
	authUC := newFakeAuthUseCase()
	srv, err := httpctrl.New(authUC, httpctrl.Config{BaseURL: "https://robin.example.com", Static: testStatic},
		httpctrl.WithGoogleWorkspace(newFakeGoogleWorkspaceUseCase()), httpctrl.WithNotion(newFakeNotionUseCase()),
		httpctrl.WithGitHub(newFakeGitHubUseCase()))
	gt.NoError(t, err).Required()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/login"},
		{http.MethodGet, "/api/auth/callback?code=c&state=s"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodGet, "/api/integrations/google-workspace"},
		{http.MethodGet, "/api/integrations/google-workspace/connect"},
		{http.MethodGet, "/api/integrations/google-workspace/callback?code=c&state=s"},
		{http.MethodPost, "/api/integrations/google-workspace/disconnect"},
		{http.MethodGet, "/api/integrations/notion"},
		{http.MethodGet, "/api/integrations/notion/connect"},
		{http.MethodGet, "/api/integrations/notion/callback?code=c&state=s"},
		{http.MethodPost, "/api/integrations/notion/disconnect"},
		{http.MethodGet, "/api/integrations/github"},
		{http.MethodGet, "/api/integrations/github/connect"},
		{http.MethodGet, "/api/integrations/github/callback?code=c&state=s"},
		{http.MethodPost, "/api/integrations/github/disconnect"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, withSession(httptest.NewRequest(tc.method, tc.path, nil), authUC))
			gt.Number(t, w.Code).Equal(http.StatusNotFound)
			gt.Value(t, decodeJSON(t, w.Body)["error"]).Equal("not_found")
		})
	}
	gt.Array(t, authUC.callbackCodes).Length(0)
}
