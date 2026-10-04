package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/domain/model/auth"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/usecase/usecasetest"
)

const (
	testBaseURL    = "https://robin.example.com"
	testUserToken  = model.SlackUserToken("xoxp-user-token")
	testSessionTTL = 7 * 24 * time.Hour
)

type fakeOAuth struct {
	result       *model.SlackOAuthResult
	err          error
	codes        []string
	redirectURIs []string
}

func (o *fakeOAuth) ExchangeCode(_ context.Context, code, redirectURI string) (*model.SlackOAuthResult, error) {
	o.codes = append(o.codes, code)
	o.redirectURIs = append(o.redirectURIs, redirectURI)
	if o.err != nil {
		return nil, o.err
	}
	res := *o.result
	return &res, nil
}

func validOAuthResult() *model.SlackOAuthResult {
	return &model.SlackOAuthResult{
		TeamID:      testKey.TeamID,
		UserID:      testKey.UserID,
		AccessToken: testUserToken,
		TokenType:   "user",
		Scopes:      []string{"search:read"},
	}
}

type authFixture struct {
	repo    *memory.Memory
	cipher  *fakeCipher
	oauth   *fakeOAuth
	bot     *usecasetest.SlackBot
	factory *fakeUserClientFactory
	access  *usecase.SlackUserAccess
	uc      *usecase.AuthUseCase
	now     time.Time
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{
		repo:    memory.New(),
		cipher:  &fakeCipher{},
		oauth:   &fakeOAuth{result: validOAuthResult()},
		bot:     usecasetest.NewSlackBot(),
		factory: newFakeUserClientFactory(),
		now:     time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
	f.bot.Names[testKey.UserID] = "Alice Example"
	f.factory.identities[testUserToken] = &model.SlackIdentity{TeamID: testKey.TeamID, UserID: testKey.UserID}
	f.access = usecase.NewSlackUserAccess(f.repo, f.cipher, f.factory)
	f.uc = usecase.NewAuthUseCase(f.repo, f.oauth, f.bot, f.access, f.factory, usecase.AuthConfig{
		ClientID:   "client-id",
		BaseURL:    testBaseURL,
		TeamID:     testKey.TeamID,
		SessionTTL: testSessionTTL,
	})
	f.uc.SetNowForTest(func() time.Time { return f.now })
	return f
}

func (f *authFixture) assertNothingStored(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := f.repo.User().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
	_, err = f.repo.SlackCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
}

func TestAuthUseCase_AuthorizeURL(t *testing.T) {
	f := newAuthFixture(t)
	raw := f.uc.AuthorizeURL("state-value")

	gt.Bool(t, strings.HasPrefix(raw, "https://slack.com/oauth/v2/authorize?")).True()
	u, err := url.Parse(raw)
	gt.NoError(t, err).Required()
	q := u.Query()
	gt.String(t, q.Get("client_id")).Equal("client-id")
	gt.String(t, q.Get("user_scope")).Equal("search:read")
	gt.String(t, q.Get("redirect_uri")).Equal(testBaseURL + "/api/v1/auth/callback")
	gt.String(t, q.Get("state")).Equal("state-value")
	gt.String(t, q.Get("team")).Equal("T0123ABCD")
	gt.Bool(t, q.Has("scope")).False()
}

func TestAuthUseCase_HandleCallback(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)

	session, secret, err := f.uc.HandleCallback(ctx, "auth-code")
	gt.NoError(t, err).Required()

	gt.Value(t, f.oauth.codes).Equal([]string{"auth-code"})
	gt.Value(t, f.oauth.redirectURIs).Equal([]string{testBaseURL + "/api/v1/auth/callback"})
	gt.Number(t, f.factory.authTestCount()).Equal(1)

	user, err := f.repo.User().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.String(t, user.Name).Equal("Alice Example")
	gt.Bool(t, user.CreatedAt.Equal(f.now)).True()
	gt.Bool(t, user.UpdatedAt.Equal(f.now)).True()

	cred, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Array(t, f.cipher.encryptions).Length(1).Required()
	gt.Value(t, f.cipher.encryptions[0].Data).Equal([]byte(testUserToken))
	gt.String(t, string(f.cipher.encryptions[0].AAD)).Equal("robin:slack-user-token:v1:T0123ABCD:U0123ABCD")
	gt.Value(t, cred.AccessToken.Ciphertext).Equal(append([]byte("robin:slack-user-token:v1:T0123ABCD:U0123ABCD|"), []byte(testUserToken)...))
	gt.Value(t, cred.Scopes).Equal([]string{"search:read"})

	stored, err := f.repo.Session().Get(ctx, session.ID)
	gt.NoError(t, err).Required()
	gt.Value(t, stored.SecretHash).Equal(secret.Hash())
	gt.Value(t, stored.Key()).Equal(testKey)
	gt.Bool(t, stored.CreatedAt.Equal(f.now)).True()
	gt.Bool(t, stored.ExpiresAt.Equal(f.now.Add(testSessionTTL))).True()
}

func TestAuthUseCase_HandleCallbackRejected(t *testing.T) {
	cases := map[string]func(f *authFixture){
		"another workspace": func(f *authFixture) { f.oauth.result.TeamID = "T9999ZZZZ" },
		"bot token type":    func(f *authFixture) { f.oauth.result.TokenType = "bot" },
		"empty token":       func(f *authFixture) { f.oauth.result.AccessToken = "" },
		"missing scope":     func(f *authFixture) { f.oauth.result.Scopes = []string{"users:read"} },
		"auth.test returns another user": func(f *authFixture) {
			f.factory.identities[testUserToken] = &model.SlackIdentity{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}
		},
		"auth.test returns another team": func(f *authFixture) {
			f.factory.identities[testUserToken] = &model.SlackIdentity{TeamID: "T9999ZZZZ", UserID: testKey.UserID}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAuthFixture(t)
			mutate(f)

			_, _, err := f.uc.HandleCallback(context.Background(), "auth-code")
			gt.Error(t, err).Is(usecase.ErrLoginRejected)
			f.assertNothingStored(t)
			gt.Array(t, f.cipher.encryptions).Length(0)
		})
	}
}

func TestAuthUseCase_HandleCallbackFailures(t *testing.T) {
	cases := map[string]func(f *authFixture){
		"code exchange fails": func(f *authFixture) { f.oauth.err = errors.New("slack unavailable") },
		"users.info fails":    func(f *authFixture) { f.bot.NameErr = errors.New("user_not_found") },
		"encryption fails":    func(f *authFixture) { f.cipher.encryptErr = errors.New("kms unavailable") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newAuthFixture(t)
			mutate(f)

			_, _, err := f.uc.HandleCallback(ctx, "auth-code")
			gt.Value(t, err).NotNil().Required()
			gt.Bool(t, errors.Is(err, usecase.ErrLoginRejected)).False()

			_, err = f.repo.SlackCredential().Get(ctx, testKey)
			gt.Error(t, err).Is(interfaces.ErrNotFound)
		})
	}
}

func TestAuthUseCase_HandleCallbackTwice(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)
	first := f.now

	session1, _, err := f.uc.HandleCallback(ctx, "code-1")
	gt.NoError(t, err).Required()

	f.now = first.Add(time.Hour)
	f.bot.Names[testKey.UserID] = "Alice Renamed"
	f.oauth.result.AccessToken = "xoxp-second-token"
	f.factory.identities["xoxp-second-token"] = &model.SlackIdentity{TeamID: testKey.TeamID, UserID: testKey.UserID}
	session2, _, err := f.uc.HandleCallback(ctx, "code-2")
	gt.NoError(t, err).Required()

	user, err := f.repo.User().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, user.CreatedAt.Equal(first)).True()
	gt.Bool(t, user.UpdatedAt.Equal(f.now)).True()
	gt.String(t, user.Name).Equal("Alice Renamed")

	cred, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, bytes.HasSuffix(cred.AccessToken.Ciphertext, []byte("xoxp-second-token"))).True()

	gt.Value(t, session1.ID).NotEqual(session2.ID)
	_, err = f.repo.Session().Get(ctx, session1.ID)
	gt.NoError(t, err)
	_, err = f.repo.Session().Get(ctx, session2.ID)
	gt.NoError(t, err)
}

func TestAuthUseCase_Authenticate(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)
	session, secret, err := f.uc.HandleCallback(ctx, "auth-code")
	gt.NoError(t, err).Required()

	t.Run("valid session", func(t *testing.T) {
		got, err := f.uc.Authenticate(ctx, session.ID, secret)
		gt.NoError(t, err).Required()
		gt.Value(t, got.ID).Equal(session.ID)
		gt.Value(t, got.Key()).Equal(testKey)
	})

	t.Run("wrong secret", func(t *testing.T) {
		_, err := f.uc.Authenticate(ctx, session.ID, auth.NewSessionSecret())
		gt.Error(t, err).Is(usecase.ErrUnauthenticated)
	})

	t.Run("unknown ID", func(t *testing.T) {
		_, err := f.uc.Authenticate(ctx, auth.NewSessionID(), secret)
		gt.Error(t, err).Is(usecase.ErrUnauthenticated)
	})

	t.Run("malformed ID", func(t *testing.T) {
		_, err := f.uc.Authenticate(ctx, "not-a-uuid", secret)
		gt.Error(t, err).Is(usecase.ErrUnauthenticated)
	})

	t.Run("expired", func(t *testing.T) {
		original := f.now
		t.Cleanup(func() { f.now = original })
		f.now = session.ExpiresAt
		_, err := f.uc.Authenticate(ctx, session.ID, secret)
		gt.Error(t, err).Is(usecase.ErrUnauthenticated)
	})
}

func TestAuthUseCase_Logout(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)
	session, secret, err := f.uc.HandleCallback(ctx, "auth-code")
	gt.NoError(t, err).Required()

	t.Run("wrong secret keeps the session", func(t *testing.T) {
		gt.NoError(t, f.uc.Logout(ctx, session.ID, auth.NewSessionSecret())).Required()
		_, err := f.uc.Authenticate(ctx, session.ID, secret)
		gt.NoError(t, err)
	})

	t.Run("unknown or malformed IDs are ignored", func(t *testing.T) {
		gt.NoError(t, f.uc.Logout(ctx, auth.NewSessionID(), secret))
		gt.NoError(t, f.uc.Logout(ctx, "not-a-uuid", secret))
	})

	t.Run("valid session is deleted and the token is kept", func(t *testing.T) {
		gt.NoError(t, f.uc.Logout(ctx, session.ID, secret)).Required()

		_, err := f.uc.Authenticate(ctx, session.ID, secret)
		gt.Error(t, err).Is(usecase.ErrUnauthenticated)
		_, err = f.repo.SlackCredential().Get(ctx, testKey)
		gt.NoError(t, err)
	})
}

func TestAuthUseCase_NoAuth(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	oauth := &fakeOAuth{err: errors.New("slack must not be called")}
	factory := newFakeUserClientFactory()
	access := usecase.NewSlackUserAccess(repo, &fakeCipher{}, factory)
	uc := usecase.NewAuthUseCase(repo, oauth, nil, access, factory, usecase.AuthConfig{
		BaseURL:      testBaseURL,
		TeamID:       testKey.TeamID,
		SessionTTL:   testSessionTTL,
		NoAuthUserID: testKey.UserID,
	})

	t.Run("authorize URL points at the own callback", func(t *testing.T) {
		u, err := url.Parse(uc.AuthorizeURL("state-value"))
		gt.NoError(t, err).Required()
		gt.String(t, u.Scheme+"://"+u.Host+u.Path).Equal(testBaseURL + "/api/v1/auth/callback")
		gt.String(t, u.Query().Get("state")).Equal("state-value")
		gt.String(t, u.Query().Get("code")).NotEqual("")
	})

	t.Run("callback signs in the configured user without Slack", func(t *testing.T) {
		session, secret, err := uc.HandleCallback(ctx, "no-auth")
		gt.NoError(t, err).Required()
		gt.Value(t, session.Key()).Equal(testKey)
		gt.Array(t, oauth.codes).Length(0)
		gt.Number(t, factory.authTestCount()).Equal(0)

		got, err := uc.Authenticate(ctx, session.ID, secret)
		gt.NoError(t, err).Required()
		gt.Value(t, got.Key()).Equal(testKey)

		me, err := uc.Me(ctx, testKey)
		gt.NoError(t, err).Required()
		gt.String(t, me.Name).Equal(string(testKey.UserID))
		gt.Bool(t, me.SlackConnected).False()
	})
}

func TestAuthUseCase_Me(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)
	_, _, err := f.uc.HandleCallback(ctx, "auth-code")
	gt.NoError(t, err).Required()

	me, err := f.uc.Me(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, me.TeamID).Equal(testKey.TeamID)
	gt.Value(t, me.UserID).Equal(testKey.UserID)
	gt.String(t, me.Name).Equal("Alice Example")
	gt.Bool(t, me.SlackConnected).True()

	client, err := f.access.Client(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.NoError(t, f.access.Disconnect(ctx, client)).Required()
	me, err = f.uc.Me(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, me.SlackConnected).False()
}
