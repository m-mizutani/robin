package github_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/github"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
)

type recordedRequest struct {
	Method        string
	Path          string
	Query         url.Values
	Header        http.Header
	Body          string
	Form          url.Values
	BasicUser     string
	BasicPassword string
}

type fakeResponse struct {
	status      int
	contentType string
	body        string
}

// fakeGitHub serves fixed responses per "METHOD path" and records every
// request.
type fakeGitHub struct {
	mu        sync.Mutex
	server    *httptest.Server
	responses map[string]fakeResponse
	requests  []recordedRequest
}

func newFakeGitHub(t *testing.T, responses map[string]fakeResponse) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{responses: responses}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		user, password, _ := r.BasicAuth()
		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Query:         r.URL.Query(),
			Header:        r.Header.Clone(),
			Body:          string(body),
			Form:          form,
			BasicUser:     user,
			BasicPassword: password,
		})
		resp, ok := f.responses[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		contentType := resp.contentType
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGitHub) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

var fixedNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func (f *fakeGitHub) oauth() *github.OAuth {
	return github.NewOAuthForTest("Iv1.client", "client-secret", f.server.URL, func() time.Time { return fixedNow })
}

const (
	redirectURI  = "https://robin.example.com/api/v1/integrations/github/callback"
	codeVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	tokenPath    = "POST /login/oauth/access_token"
	expiringBody = `{"access_token":"ghu_access","expires_in":28800,"refresh_token":"ghr_refresh","refresh_token_expires_in":15897600,"scope":"","token_type":"bearer"}`
)

func TestOAuth_AuthorizeURL(t *testing.T) {
	o := github.NewOAuthForTest("Iv1.client", "client-secret", "https://github.example", time.Now)
	u, err := url.Parse(o.AuthorizeURL(redirectURI, "state-1", codeVerifier))
	gt.NoError(t, err).Required()

	gt.String(t, u.Scheme+"://"+u.Host+u.Path).Equal("https://github.example/login/oauth/authorize")
	q := u.Query()
	gt.String(t, q.Get("client_id")).Equal("Iv1.client")
	gt.String(t, q.Get("redirect_uri")).Equal(redirectURI)
	gt.String(t, q.Get("state")).Equal("state-1")
	gt.String(t, q.Get("response_type")).Equal("code")
	gt.String(t, q.Get("code_challenge_method")).Equal("S256")
	gt.String(t, q.Get("allow_signup")).Equal("false")
	sum := sha256.Sum256([]byte(codeVerifier))
	gt.String(t, q.Get("code_challenge")).Equal(base64.RawURLEncoding.EncodeToString(sum[:]))
}

func TestOAuth_AuthorizeURLForGitHub(t *testing.T) {
	o := github.NewOAuth("Iv1.client", "client-secret", http.DefaultClient)
	u, err := url.Parse(o.AuthorizeURL(redirectURI, "s", codeVerifier))
	gt.NoError(t, err).Required()
	gt.String(t, u.Scheme+"://"+u.Host+u.Path).Equal("https://github.com/login/oauth/authorize")
}

func TestOAuth_ExchangeCode(t *testing.T) {
	t.Run("expiring tokens as JSON", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {status: http.StatusOK, body: expiringBody}})

		tok, err := f.oauth().ExchangeCode(context.Background(), "code-1", redirectURI, codeVerifier)
		gt.NoError(t, err).Required()
		gt.Value(t, tok.AccessToken).Equal("ghu_access")
		gt.Value(t, tok.RefreshToken).Equal("ghr_refresh")
		gt.Value(t, tok.AccessTokenExpiresAt).Equal(fixedNow.Add(28800 * time.Second))
		gt.Value(t, tok.RefreshTokenExpiresAt).Equal(fixedNow.Add(15897600 * time.Second))

		reqs := f.recorded()
		gt.Array(t, reqs).Length(1).Required()
		gt.String(t, reqs[0].Form.Get("client_id")).Equal("Iv1.client")
		gt.String(t, reqs[0].Form.Get("client_secret")).Equal("client-secret")
		gt.String(t, reqs[0].Form.Get("code")).Equal("code-1")
		gt.String(t, reqs[0].Form.Get("redirect_uri")).Equal(redirectURI)
		gt.String(t, reqs[0].Form.Get("code_verifier")).Equal(codeVerifier)
		gt.String(t, reqs[0].Form.Get("grant_type")).Equal("authorization_code")
	})

	t.Run("expiring tokens as a form body", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {
			status:      http.StatusOK,
			contentType: "application/x-www-form-urlencoded",
			body:        "access_token=ghu_access&expires_in=28800&refresh_token=ghr_refresh&refresh_token_expires_in=15897600&scope=&token_type=bearer",
		}})

		tok, err := f.oauth().ExchangeCode(context.Background(), "code-1", redirectURI, codeVerifier)
		gt.NoError(t, err).Required()
		gt.Value(t, tok.AccessTokenExpiresAt).Equal(fixedNow.Add(28800 * time.Second))
		gt.Value(t, tok.RefreshTokenExpiresAt).Equal(fixedNow.Add(15897600 * time.Second))
	})

	t.Run("tokens that do not expire", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {
			status: http.StatusOK,
			body:   `{"access_token":"ghu_access","scope":"","token_type":"bearer"}`,
		}})

		tok, err := f.oauth().ExchangeCode(context.Background(), "code-1", redirectURI, codeVerifier)
		gt.NoError(t, err).Required()
		gt.Value(t, tok.AccessToken).Equal("ghu_access")
		gt.Value(t, tok.RefreshToken).Equal("")
		gt.Bool(t, tok.AccessTokenExpiresAt.IsZero()).True()
		gt.Bool(t, tok.RefreshTokenExpiresAt.IsZero()).True()
	})

	t.Run("bad verification code in a 200 response", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {
			status: http.StatusOK,
			body:   `{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired."}`,
		}})

		_, err := f.oauth().ExchangeCode(context.Background(), "secret-code", redirectURI, codeVerifier)
		gt.Error(t, err)
		gt.Bool(t, errors.Is(err, interfaces.ErrGitHubTokenInvalid)).False()
		gt.Bool(t, strings.Contains(err.Error(), "secret-code")).False()
	})
}

func TestOAuth_Refresh(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {status: http.StatusOK, body: expiringBody}})

		tok, err := f.oauth().Refresh(context.Background(), "ghr_old")
		gt.NoError(t, err).Required()
		gt.Value(t, tok.AccessToken).Equal("ghu_access")
		gt.Value(t, tok.RefreshToken).Equal("ghr_refresh")
		gt.Value(t, tok.AccessTokenExpiresAt).Equal(fixedNow.Add(28800 * time.Second))
		gt.Value(t, tok.RefreshTokenExpiresAt).Equal(fixedNow.Add(15897600 * time.Second))

		reqs := f.recorded()
		gt.Array(t, reqs).Length(1).Required()
		gt.String(t, reqs[0].Form.Get("grant_type")).Equal("refresh_token")
		gt.String(t, reqs[0].Form.Get("refresh_token")).Equal("ghr_old")
		gt.String(t, reqs[0].Form.Get("client_id")).Equal("Iv1.client")
		gt.String(t, reqs[0].Form.Get("client_secret")).Equal("client-secret")
	})

	t.Run("bad refresh token", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {
			status: http.StatusOK,
			body:   `{"error":"bad_refresh_token","error_description":"The refresh token passed is incorrect or expired."}`,
		}})

		_, err := f.oauth().Refresh(context.Background(), "ghr_old")
		gt.Error(t, err).Is(interfaces.ErrGitHubTokenInvalid)
	})

	t.Run("server error", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{tokenPath: {status: http.StatusInternalServerError, body: `{}`}})

		_, err := f.oauth().Refresh(context.Background(), "ghr_old")
		gt.Error(t, err)
		gt.Bool(t, errors.Is(err, interfaces.ErrGitHubTokenInvalid)).False()
	})
}

func TestOAuth_RevokeGrant(t *testing.T) {
	const grantPath = "DELETE /applications/Iv1.client/grant"

	t.Run("deleted", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{grantPath: {status: http.StatusNoContent}})

		gt.NoError(t, f.oauth().RevokeGrant(context.Background(), "ghu_access"))

		reqs := f.recorded()
		gt.Array(t, reqs).Length(1).Required()
		gt.String(t, reqs[0].BasicUser).Equal("Iv1.client")
		gt.String(t, reqs[0].BasicPassword).Equal("client-secret")
		gt.String(t, reqs[0].Body).Equal(`{"access_token":"ghu_access"}`)
		gt.String(t, reqs[0].Header.Get("Accept")).Equal("application/vnd.github+json")
		gt.String(t, reqs[0].Header.Get("X-GitHub-Api-Version")).Equal("2026-03-10")
	})

	t.Run("unknown token", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{grantPath: {status: http.StatusNotFound, body: `{}`}})
		gt.Error(t, f.oauth().RevokeGrant(context.Background(), "ghu_access")).Is(interfaces.ErrGitHubTokenInvalid)
	})

	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newFakeGitHub(t, map[string]fakeResponse{grantPath: {status: status, body: `{}`}})
			err := f.oauth().RevokeGrant(context.Background(), "ghu_access")
			gt.Error(t, err)
			gt.Bool(t, errors.Is(err, interfaces.ErrGitHubTokenInvalid)).False()
		})
	}
}
