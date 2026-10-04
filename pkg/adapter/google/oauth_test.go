package google_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/google"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type recordedRequest struct {
	Method        string
	Path          string
	Query         url.Values
	ContentType   string
	Authorization string
	Form          url.Values
}

type fakeResponse struct {
	status int
	body   string
}

// fakeGoogle serves fixed responses per path and records every request. A
// download (alt=media) of a path is answered by the entry path+"#media".
type fakeGoogle struct {
	mu        sync.Mutex
	server    *httptest.Server
	responses map[string]fakeResponse
	requests  []recordedRequest
}

func newFakeGoogle(t *testing.T, responses map[string]fakeResponse) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{responses: responses}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Query:         r.URL.Query(),
			ContentType:   r.Header.Get("Content-Type"),
			Authorization: r.Header.Get("Authorization"),
			Form:          form,
		})
		key := r.URL.Path
		if r.URL.Query().Get("alt") == "media" {
			key += "#media"
		}
		resp, ok := f.responses[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGoogle) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func (f *fakeGoogle) oauth() *google.OAuth {
	return google.NewOAuthForTest("client-id", "client-secret", f.server.URL)
}

const redirectURI = "https://robin.example.com/api/v1/integrations/google-workspace/callback"

var scopes = []string{
	"openid",
	"email",
	"https://www.googleapis.com/auth/calendar.readonly",
	"https://www.googleapis.com/auth/drive.readonly",
	"https://www.googleapis.com/auth/gmail.readonly",
}

func TestOAuth_AuthorizeURL(t *testing.T) {
	raw := google.NewOAuth("client-id", "client-secret").AuthorizeURL("s1", redirectURI, scopes)
	u, err := url.Parse(raw)
	gt.NoError(t, err).Required()

	gt.String(t, u.Scheme).Equal("https")
	gt.String(t, u.Host).Equal("accounts.google.com")
	gt.String(t, u.Path).Equal("/o/oauth2/v2/auth")
	q := u.Query()
	gt.String(t, q.Get("client_id")).Equal("client-id")
	gt.String(t, q.Get("redirect_uri")).Equal(redirectURI)
	gt.String(t, q.Get("response_type")).Equal("code")
	gt.Value(t, strings.Fields(q.Get("scope"))).Equal(scopes)
	gt.String(t, q.Get("state")).Equal("s1")
	gt.String(t, q.Get("access_type")).Equal("offline")
	gt.String(t, q.Get("prompt")).Equal("consent")
	gt.Bool(t, q.Has("include_granted_scopes")).False()
	gt.Bool(t, q.Has("client_secret")).False()
}

func TestOAuth_ExchangeCode(t *testing.T) {
	fake := newFakeGoogle(t, map[string]fakeResponse{
		"/token": {status: http.StatusOK, body: `{
			"access_token": "access-1",
			"refresh_token": "refresh-1",
			"scope": "openid https://www.googleapis.com/auth/gmail.readonly",
			"expires_in": 3599,
			"token_type": "Bearer"
		}`},
	})

	got, err := fake.oauth().ExchangeCode(context.Background(), "auth-code", redirectURI)
	gt.NoError(t, err).Required()
	gt.Value(t, got.AccessToken).Equal(model.GoogleAccessToken("access-1"))
	gt.Value(t, got.RefreshToken).Equal(model.GoogleRefreshToken("refresh-1"))
	gt.Value(t, got.Scopes).Equal([]string{"openid", "https://www.googleapis.com/auth/gmail.readonly"})

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Method).Equal(http.MethodPost)
	gt.String(t, reqs[0].Path).Equal("/token")
	gt.String(t, reqs[0].Form.Get("grant_type")).Equal("authorization_code")
	gt.String(t, reqs[0].Form.Get("code")).Equal("auth-code")
	gt.String(t, reqs[0].Form.Get("redirect_uri")).Equal(redirectURI)
	gt.String(t, reqs[0].Form.Get("client_id")).Equal("client-id")
	gt.String(t, reqs[0].Form.Get("client_secret")).Equal("client-secret")
}

func TestOAuth_ExchangeCodeWithoutRefreshToken(t *testing.T) {
	fake := newFakeGoogle(t, map[string]fakeResponse{
		"/token": {status: http.StatusOK, body: `{
			"access_token": "access-1",
			"scope": "openid",
			"expires_in": 3599,
			"token_type": "Bearer"
		}`},
	})

	got, err := fake.oauth().ExchangeCode(context.Background(), "auth-code", redirectURI)
	gt.NoError(t, err).Required()
	gt.Value(t, got.AccessToken).Equal(model.GoogleAccessToken("access-1"))
	gt.Value(t, got.RefreshToken).Equal(model.GoogleRefreshToken(""))
}

func TestOAuth_ExchangeCodeError(t *testing.T) {
	fake := newFakeGoogle(t, map[string]fakeResponse{
		"/token": {status: http.StatusBadRequest, body: `{"error": "invalid_grant", "error_description": "Bad Request"}`},
	})

	_, err := fake.oauth().ExchangeCode(context.Background(), "secret-auth-code", redirectURI)
	gt.Error(t, err)
	gt.Bool(t, strings.Contains(err.Error(), "secret-auth-code")).False()
}

func TestOAuth_FetchIdentity(t *testing.T) {
	fake := newFakeGoogle(t, map[string]fakeResponse{
		"/userinfo": {status: http.StatusOK, body: `{"sub": "1234567890", "email": "alice@example.com", "email_verified": true}`},
	})

	got, err := fake.oauth().FetchIdentity(context.Background(), "access-1")
	gt.NoError(t, err).Required()
	gt.Value(t, got).Equal(&model.GoogleIdentity{Subject: "1234567890", Email: "alice@example.com"})

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Method).Equal(http.MethodGet)
	gt.String(t, reqs[0].Authorization).Equal("Bearer access-1")
}

func TestOAuth_FetchIdentityError(t *testing.T) {
	fake := newFakeGoogle(t, map[string]fakeResponse{
		"/userinfo": {status: http.StatusUnauthorized, body: `{"error": "invalid_token"}`},
	})

	_, err := fake.oauth().FetchIdentity(context.Background(), "access-1")
	gt.Error(t, err)
}

func TestOAuth_Revoke(t *testing.T) {
	cases := map[string]struct {
		status      int
		wantErr     bool
		wantInvalid bool
	}{
		"revoked":            {status: http.StatusOK},
		"token rejected":     {status: http.StatusBadRequest, wantErr: true, wantInvalid: true},
		"google unavailable": {status: http.StatusServiceUnavailable, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeGoogle(t, map[string]fakeResponse{
				"/revoke": {status: tc.status, body: `{}`},
			})

			err := fake.oauth().Revoke(context.Background(), "refresh-1")
			if tc.wantErr {
				gt.Error(t, err)
				gt.Value(t, errors.Is(err, interfaces.ErrGoogleTokenInvalid)).Equal(tc.wantInvalid)
			} else {
				gt.NoError(t, err)
			}

			reqs := fake.recorded()
			gt.Array(t, reqs).Length(1).Required()
			gt.String(t, reqs[0].Method).Equal(http.MethodPost)
			gt.String(t, reqs[0].ContentType).Equal("application/x-www-form-urlencoded")
			gt.String(t, reqs[0].Form.Get("token")).Equal("refresh-1")
		})
	}
}
