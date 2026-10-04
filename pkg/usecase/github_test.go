package usecase_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
)

const githubCallbackURL = "https://robin.example.com/api/v1/integrations/github/callback"

var octocat = &model.GitHubIdentity{ID: 583231, Login: "octocat"}

type githubAuthorizeCall struct {
	RedirectURI  string
	State        string
	CodeVerifier string
}

type githubExchangeCall struct {
	Code         string
	RedirectURI  string
	CodeVerifier string
}

// fakeGitHubOAuth returns configured results and records every call Robin
// would make to GitHub.
type fakeGitHubOAuth struct {
	mu          sync.Mutex
	token       *model.GitHubToken
	exchangeErr error
	// refresh answers a refresh; nil returns a new expiring token.
	refresh   func(refreshToken model.GitHubRefreshToken) (*model.GitHubToken, error)
	revokeErr error

	authorizes []githubAuthorizeCall
	exchanges  []githubExchangeCall
	refreshes  []model.GitHubRefreshToken
	// refreshDeadlines records the context deadline of every refresh call.
	refreshDeadlines []refreshDeadline
	revokes          []model.GitHubAccessToken
	now              func() time.Time
}

type refreshDeadline struct {
	deadline time.Time
	ok       bool
}

func expiringToken(now time.Time, suffix string) *model.GitHubToken {
	return &model.GitHubToken{
		AccessToken:           model.GitHubAccessToken("ghu_" + suffix),
		AccessTokenExpiresAt:  now.Add(8 * time.Hour),
		RefreshToken:          model.GitHubRefreshToken("ghr_" + suffix),
		RefreshTokenExpiresAt: now.Add(4416 * time.Hour),
	}
}

func (f *fakeGitHubOAuth) AuthorizeURL(redirectURI, state, codeVerifier string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizes = append(f.authorizes, githubAuthorizeCall{RedirectURI: redirectURI, State: state, CodeVerifier: codeVerifier})
	return "https://github.com/login/oauth/authorize?state=" + state
}

func (f *fakeGitHubOAuth) ExchangeCode(_ context.Context, code, redirectURI, codeVerifier string) (*model.GitHubToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges = append(f.exchanges, githubExchangeCall{Code: code, RedirectURI: redirectURI, CodeVerifier: codeVerifier})
	if f.exchangeErr != nil {
		return nil, f.exchangeErr
	}
	tok := *f.token
	return &tok, nil
}

func (f *fakeGitHubOAuth) Refresh(ctx context.Context, refreshToken model.GitHubRefreshToken) (*model.GitHubToken, error) {
	f.mu.Lock()
	f.refreshes = append(f.refreshes, refreshToken)
	deadline, ok := ctx.Deadline()
	f.refreshDeadlines = append(f.refreshDeadlines, refreshDeadline{deadline: deadline, ok: ok})
	n := len(f.refreshes)
	fn := f.refresh
	f.mu.Unlock()
	if fn != nil {
		return fn(refreshToken)
	}
	return expiringToken(f.now(), "refreshed"+strconv.Itoa(n)), nil
}

func (f *fakeGitHubOAuth) RevokeGrant(_ context.Context, accessToken model.GitHubAccessToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes = append(f.revokes, accessToken)
	return f.revokeErr
}

func (f *fakeGitHubOAuth) refreshed() []model.GitHubRefreshToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.GitHubRefreshToken(nil), f.refreshes...)
}

func (f *fakeGitHubOAuth) revoked() []model.GitHubAccessToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.GitHubAccessToken(nil), f.revokes...)
}

// fakeGitHubUsers answers GET /user for every token.
type fakeGitHubUsers struct {
	mu       sync.Mutex
	identity *model.GitHubIdentity
	err      error
	tokens   []model.GitHubAccessToken
}

func (f *fakeGitHubUsers) New(token model.GitHubAccessToken) interfaces.GitHubUserClient {
	return &fakeGitHubUserClient{users: f, token: token}
}

type fakeGitHubUserClient struct {
	users *fakeGitHubUsers
	token model.GitHubAccessToken
}

// The read methods are not used by the tests of this package; the agents
// test the reads with their own clients.
var errGitHubReadNotUsed = errors.New("github reads are not used in usecase tests")

func (c *fakeGitHubUserClient) SearchIssues(context.Context, string, int) ([]model.GitHubIssueSummary, error) {
	return nil, errGitHubReadNotUsed
}

func (c *fakeGitHubUserClient) SearchCode(context.Context, string, int) ([]model.GitHubCodeHit, error) {
	return nil, errGitHubReadNotUsed
}

func (c *fakeGitHubUserClient) GetIssue(context.Context, string, string, int) (*model.GitHubIssue, error) {
	return nil, errGitHubReadNotUsed
}

func (c *fakeGitHubUserClient) GetContent(context.Context, string, string, string, string) (*model.GitHubContent, error) {
	return nil, errGitHubReadNotUsed
}

func (c *fakeGitHubUserClient) GetUser(context.Context) (*model.GitHubIdentity, error) {
	c.users.mu.Lock()
	defer c.users.mu.Unlock()
	c.users.tokens = append(c.users.tokens, c.token)
	if c.users.err != nil {
		return nil, c.users.err
	}
	id := *c.users.identity
	return &id, nil
}

type githubFixture struct {
	mu     sync.Mutex
	repo   *memory.Memory
	cipher *fakeCipher
	oauth  *fakeGitHubOAuth
	users  *fakeGitHubUsers
	access *usecase.GitHubUserAccess
	uc     *usecase.GitHubUseCase
	now    time.Time
	ids    int
	// onSleep runs on every wait of GitHubUserAccess, before the clock
	// advances by the wait.
	onSleep func()
}

func newGitHubFixture() *githubFixture {
	f := &githubFixture{
		repo:   memory.New(),
		cipher: &fakeCipher{},
		users:  &fakeGitHubUsers{identity: octocat},
		now:    time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
	}
	f.oauth = &fakeGitHubOAuth{now: f.clock}
	f.oauth.token = expiringToken(f.now, "first")
	f.access = usecase.NewGitHubUserAccess(f.repo, f.cipher, f.oauth, f.users)
	f.access.SetClockForTest(f.clock, f.sleep, f.newID)
	f.uc = usecase.NewGitHubUseCase(f.oauth, f.users, f.access, usecase.GitHubConfig{BaseURL: "https://robin.example.com"})
	return f
}

func (f *githubFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *githubFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *githubFixture) sleep(_ context.Context, d time.Duration) error {
	if f.onSleep != nil {
		f.onSleep()
	}
	f.advance(d)
	return nil
}

func (f *githubFixture) newID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids++
	return "id-" + strconv.Itoa(f.ids)
}

func (f *githubFixture) stored(t *testing.T) *model.GitHubCredential {
	t.Helper()
	cred, err := f.repo.GitHubCredential().Get(context.Background(), testKey)
	gt.NoError(t, err).Required()
	return cred
}

func (f *githubFixture) assertNothingStored(t *testing.T) {
	t.Helper()
	_, err := f.repo.GitHubCredential().Get(context.Background(), testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
	inUse, err := f.repo.GitHubCredential().AccountInUse(context.Background(), otherKey, octocat.ID)
	gt.NoError(t, err).Required()
	gt.Bool(t, inUse).False()
}

// connect stores a connection of testKey through the callback.
func (f *githubFixture) connect(t *testing.T) {
	t.Helper()
	gt.NoError(t, f.uc.HandleCallback(context.Background(), testKey, "code-1", "verifier-1")).Required()
}

func TestGitHubUseCase_AuthorizeURL(t *testing.T) {
	t.Run("not connected", func(t *testing.T) {
		f := newGitHubFixture()
		got, err := f.uc.AuthorizeURL(context.Background(), testKey, "state-1", "verifier-1")
		gt.NoError(t, err).Required()
		gt.String(t, got).Equal("https://github.com/login/oauth/authorize?state=state-1")
		gt.Value(t, f.oauth.authorizes).Equal([]githubAuthorizeCall{
			{RedirectURI: githubCallbackURL, State: "state-1", CodeVerifier: "verifier-1"},
		})
	})

	t.Run("already connected", func(t *testing.T) {
		f := newGitHubFixture()
		f.connect(t)
		_, err := f.uc.AuthorizeURL(context.Background(), testKey, "state-1", "verifier-1")
		gt.Error(t, err).Is(usecase.ErrGitHubAlreadyConnected)
		gt.Array(t, f.oauth.authorizes).Length(0)
	})

	t.Run("connection whose refresh token expired", func(t *testing.T) {
		f := newGitHubFixture()
		f.connect(t)
		f.advance(4417 * time.Hour)
		_, err := f.uc.AuthorizeURL(context.Background(), testKey, "state-1", "verifier-1")
		gt.NoError(t, err)
	})
}

func TestGitHubUseCase_HandleCallback(t *testing.T) {
	f := newGitHubFixture()
	f.connect(t)

	gt.Value(t, f.oauth.exchanges).Equal([]githubExchangeCall{
		{Code: "code-1", RedirectURI: githubCallbackURL, CodeVerifier: "verifier-1"},
	})
	gt.Value(t, f.users.tokens).Equal([]model.GitHubAccessToken{"ghu_first"})
	gt.Array(t, f.oauth.revoked()).Length(0)

	cred := f.stored(t)
	gt.String(t, cred.ConnectionID).Equal("id-1")
	gt.Value(t, cred.GitHubUserID).Equal(octocat.ID)
	gt.String(t, cred.GitHubLogin).Equal("octocat")
	gt.Value(t, cred.AccessTokenExpiresAt).Equal(f.now.Add(8 * time.Hour))
	gt.Value(t, cred.RefreshTokenExpiresAt).Equal(f.now.Add(4416 * time.Hour))
	gt.Value(t, cred.CreatedAt).Equal(f.now)
	gt.Value(t, cred.UpdatedAt).Equal(f.now)

	access, err := f.cipher.Decrypt(context.Background(), &cred.AccessToken, usecase.GitHubAccessTokenAADForTest(testKey))
	gt.NoError(t, err).Required()
	gt.String(t, string(access)).Equal("ghu_first")
	refresh, err := f.cipher.Decrypt(context.Background(), cred.RefreshToken, usecase.GitHubRefreshTokenAADForTest(testKey))
	gt.NoError(t, err).Required()
	gt.String(t, string(refresh)).Equal("ghr_first")
	gt.String(t, string(usecase.GitHubAccessTokenAADForTest(testKey))).Equal("robin:github-access-token:v1:T0123ABCD:U0123ABCD")
	gt.String(t, string(usecase.GitHubRefreshTokenAADForTest(testKey))).Equal("robin:github-refresh-token:v1:T0123ABCD:U0123ABCD")

	inUse, err := f.repo.GitHubCredential().AccountInUse(context.Background(), otherKey, octocat.ID)
	gt.NoError(t, err).Required()
	gt.Bool(t, inUse).True()
}

func TestGitHubUseCase_HandleCallbackTokensThatDoNotExpire(t *testing.T) {
	f := newGitHubFixture()
	f.oauth.token = &model.GitHubToken{AccessToken: "ghu_forever"}
	f.connect(t)

	cred := f.stored(t)
	gt.Value(t, cred.RefreshToken).Nil()
	gt.Bool(t, cred.AccessTokenExpiresAt.IsZero()).True()
}

func TestGitHubUseCase_HandleCallbackRejections(t *testing.T) {
	cases := map[string]struct {
		setup      func(t *testing.T, f *githubFixture)
		wantErr    error
		wantRevoke bool
	}{
		"exchange fails": {
			setup: func(_ *testing.T, f *githubFixture) { f.oauth.exchangeErr = errors.New("bad_verification_code") },
		},
		"identity cannot be read": {
			setup: func(_ *testing.T, f *githubFixture) { f.users.err = errors.New("github unavailable") },
		},
		"identity is incomplete": {
			setup:   func(_ *testing.T, f *githubFixture) { f.users.identity = &model.GitHubIdentity{ID: 583231} },
			wantErr: usecase.ErrGitHubConnectRejected,
		},
		"github account connected to another user": {
			setup: func(t *testing.T, f *githubFixture) {
				other := newGitHubFixture()
				gt.NoError(t, other.uc.HandleCallback(context.Background(), otherKey, "c", "v")).Required()
				cred, err := other.repo.GitHubCredential().Get(context.Background(), otherKey)
				gt.NoError(t, err).Required()
				gt.NoError(t, f.repo.GitHubCredential().Create(context.Background(), otherKey, cred)).Required()
			},
			wantErr: usecase.ErrGitHubAccountInUse,
		},
		"token has only part of the expiry": {
			setup: func(_ *testing.T, f *githubFixture) {
				f.oauth.token = &model.GitHubToken{AccessToken: "ghu_first", AccessTokenExpiresAt: f.now.Add(time.Hour)}
			},
			wantErr:    usecase.ErrGitHubConnectRejected,
			wantRevoke: true,
		},
		"store fails": {
			setup: func(_ *testing.T, f *githubFixture) { f.cipher.encryptErr = errors.New("kms unavailable") },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newGitHubFixture()
			tc.setup(t, f)

			err := f.uc.HandleCallback(context.Background(), testKey, "code-1", "verifier-1")
			gt.Error(t, err)
			if tc.wantErr != nil {
				gt.Error(t, err).Is(tc.wantErr)
			}
			_, getErr := f.repo.GitHubCredential().Get(context.Background(), testKey)
			gt.Error(t, getErr).Is(interfaces.ErrNotFound)
			if tc.wantRevoke {
				gt.Value(t, f.oauth.revoked()).Equal([]model.GitHubAccessToken{"ghu_first"})
			} else {
				gt.Array(t, f.oauth.revoked()).Length(0)
			}
		})
	}
}

func TestGitHubUseCase_HandleCallbackRevokeFailureKeepsTheCause(t *testing.T) {
	f := newGitHubFixture()
	f.oauth.token = &model.GitHubToken{AccessToken: "ghu_first", AccessTokenExpiresAt: f.now.Add(time.Hour)}
	f.oauth.revokeErr = errors.New("github unavailable")

	err := f.uc.HandleCallback(context.Background(), testKey, "code-1", "verifier-1")
	gt.Error(t, err).Is(usecase.ErrGitHubConnectRejected)
	gt.Array(t, f.oauth.revoked()).Length(1)
	f.assertNothingStored(t)
}

// Another user connects the same GitHub account between the owner check and
// the revocation of an invalid token. The revocation is skipped, because it
// would end that user's connection.
func TestGitHubUseCase_HandleCallbackRejectKeepsAnotherUsersConnection(t *testing.T) {
	f := newGitHubFixture()
	f.oauth.token = &model.GitHubToken{AccessToken: "ghu_first", AccessTokenExpiresAt: f.now.Add(time.Hour)}

	other := newGitHubFixture()
	gt.NoError(t, other.uc.HandleCallback(context.Background(), otherKey, "c", "v")).Required()
	otherCred, err := other.repo.GitHubCredential().Get(context.Background(), otherKey)
	gt.NoError(t, err).Required()

	creds := &hookedGitHubCredentials{GitHubCredentialRepository: f.repo.GitHubCredential()}
	creds.afterFirstAccountCheck = func() {
		gt.NoError(t, creds.Create(context.Background(), otherKey, otherCred)).Required()
	}
	repo := &hookedRepository{Memory: f.repo, github: creds}
	access := usecase.NewGitHubUserAccess(repo, f.cipher, f.oauth, f.users)
	uc := usecase.NewGitHubUseCase(f.oauth, f.users, access, usecase.GitHubConfig{BaseURL: "https://robin.example.com"})

	err = uc.HandleCallback(context.Background(), testKey, "code-1", "verifier-1")
	gt.Error(t, err).Is(usecase.ErrGitHubConnectRejected)
	gt.Array(t, f.oauth.revoked()).Length(0)
	gt.Number(t, creds.accountChecks).Equal(2)
}

// hookedRepository returns a GitHub credential repository that runs a hook
// after its first AccountInUse call.
type hookedRepository struct {
	*memory.Memory
	github *hookedGitHubCredentials
}

func (r *hookedRepository) GitHubCredential() interfaces.GitHubCredentialRepository { return r.github }

type hookedGitHubCredentials struct {
	interfaces.GitHubCredentialRepository
	afterFirstAccountCheck func()
	accountChecks          int
}

func (h *hookedGitHubCredentials) AccountInUse(ctx context.Context, key model.UserKey, id model.GitHubUserID) (bool, error) {
	inUse, err := h.GitHubCredentialRepository.AccountInUse(ctx, key, id)
	h.accountChecks++
	if h.accountChecks == 1 && h.afterFirstAccountCheck != nil {
		h.afterFirstAccountCheck()
	}
	return inUse, err
}

func TestGitHubUseCase_HandleCallbackAlreadyConnected(t *testing.T) {
	f := newGitHubFixture()
	f.connect(t)

	err := f.uc.HandleCallback(context.Background(), testKey, "code-2", "verifier-2")
	gt.Error(t, err).Is(usecase.ErrGitHubAlreadyConnected)
	gt.Array(t, f.oauth.exchanges).Length(1)
	gt.String(t, f.stored(t).ConnectionID).Equal("id-1")
}

// A concurrent callback of the same user stores its connection between the
// check and the store. The second one is refused without revoking, because
// the authorization may be the one the first callback stored.
func TestGitHubUseCase_HandleCallbackRacingSameUser(t *testing.T) {
	f := newGitHubFixture()
	f.users.identity = octocat
	// The first GET /user of this callback lets another callback finish.
	var once sync.Once
	f.users.err = nil
	hooked := &hookedUsers{fakeGitHubUsers: f.users, before: func() {
		once.Do(func() {
			other := newGitHubFixture()
			other.connect(t)
			gt.NoError(t, f.repo.GitHubCredential().Create(context.Background(), testKey, other.stored(t))).Required()
		})
	}}
	uc := usecase.NewGitHubUseCase(f.oauth, hooked, f.access, usecase.GitHubConfig{BaseURL: "https://robin.example.com"})

	err := uc.HandleCallback(context.Background(), testKey, "code-2", "verifier-2")
	gt.Error(t, err).Is(usecase.ErrGitHubAlreadyConnected)
	gt.Array(t, f.oauth.revoked()).Length(0)
}

type hookedUsers struct {
	*fakeGitHubUsers
	before func()
}

func (h *hookedUsers) New(token model.GitHubAccessToken) interfaces.GitHubUserClient {
	h.before()
	return h.fakeGitHubUsers.New(token)
}

func TestGitHubUseCase_HandleCallbackReplacesExpiredConnection(t *testing.T) {
	f := newGitHubFixture()
	f.connect(t)
	f.advance(4417 * time.Hour)
	f.oauth.token = expiringToken(f.now, "second")

	gt.NoError(t, f.uc.HandleCallback(context.Background(), testKey, "code-2", "verifier-2")).Required()
	cred := f.stored(t)
	gt.String(t, cred.ConnectionID).Equal("id-2")
	gt.Value(t, cred.CreatedAt).Equal(f.now)
}

func TestGitHubUseCase_Status(t *testing.T) {
	f := newGitHubFixture()

	status, err := f.uc.Status(context.Background(), testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GitHubStatus{})

	f.connect(t)
	status, err = f.uc.Status(context.Background(), testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GitHubStatus{Connected: true, Login: "octocat"})

	f.advance(4416 * time.Hour)
	status, err = f.uc.Status(context.Background(), testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GitHubStatus{})
	// Reading the status deletes nothing.
	gt.String(t, f.stored(t).ConnectionID).Equal("id-1")
	gt.Array(t, f.oauth.refreshed()).Length(0)
	gt.Array(t, f.oauth.revoked()).Length(0)
}

func TestGitHubUseCase_Disconnect(t *testing.T) {
	t.Run("deletes the authorization, then the connection", func(t *testing.T) {
		f := newGitHubFixture()
		f.connect(t)

		gt.NoError(t, f.uc.Disconnect(context.Background(), testKey)).Required()
		gt.Value(t, f.oauth.revoked()).Equal([]model.GitHubAccessToken{"ghu_first"})
		f.assertNothingStored(t)
	})

	t.Run("refreshes an access token about to expire first", func(t *testing.T) {
		f := newGitHubFixture()
		f.connect(t)
		f.advance(8*time.Hour - time.Minute)

		gt.NoError(t, f.uc.Disconnect(context.Background(), testKey)).Required()
		gt.Value(t, f.oauth.refreshed()).Equal([]model.GitHubRefreshToken{"ghr_first"})
		gt.Value(t, f.oauth.revoked()).Equal([]model.GitHubAccessToken{"ghu_refreshed1"})
		f.assertNothingStored(t)
	})

	t.Run("token already invalid at GitHub", func(t *testing.T) {
		f := newGitHubFixture()
		f.connect(t)
		f.oauth.revokeErr = goerr.Wrap(interfaces.ErrGitHubTokenInvalid, "unknown token")

		gt.NoError(t, f.uc.Disconnect(context.Background(), testKey)).Required()
		f.assertNothingStored(t)
	})

	t.Run("GitHub cannot be reached", func(t *testing.T) {
		f := newGitHubFixture()
		f.connect(t)
		f.oauth.revokeErr = errors.New("github unavailable")

		gt.Error(t, f.uc.Disconnect(context.Background(), testKey))
		gt.String(t, f.stored(t).ConnectionID).Equal("id-1")
	})

	t.Run("not connected", func(t *testing.T) {
		f := newGitHubFixture()
		gt.NoError(t, f.uc.Disconnect(context.Background(), testKey))
		gt.Array(t, f.oauth.revoked()).Length(0)
	})
}
