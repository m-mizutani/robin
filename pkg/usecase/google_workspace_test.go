package usecase_test

import (
	"bytes"
	"context"
	"errors"
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

const googleCallbackURL = "https://robin.example.com/api/v1/integrations/google-workspace/callback"

type authorizeCall struct {
	State       string
	RedirectURI string
	Scopes      []string
}

type exchangeCall struct {
	Code        string
	RedirectURI string
}

// fakeGoogleOAuth returns configured results and records every call Robin
// would make to Google.
type fakeGoogleOAuth struct {
	mu          sync.Mutex
	result      *model.GoogleOAuthResult
	exchangeErr error
	identity    *model.GoogleIdentity
	identityErr error
	revokeErr   error

	authorizes []authorizeCall
	exchanges  []exchangeCall
	identities []model.GoogleAccessToken
	revokes    []string
}

func newFakeGoogleOAuth() *fakeGoogleOAuth {
	return &fakeGoogleOAuth{
		result: &model.GoogleOAuthResult{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			Scopes:       googleScopes,
		},
		identity: aliceIdentity,
	}
}

func (f *fakeGoogleOAuth) AuthorizeURL(state, redirectURI string, scopes []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizes = append(f.authorizes, authorizeCall{State: state, RedirectURI: redirectURI, Scopes: scopes})
	return "https://accounts.google.com/o/oauth2/v2/auth?state=" + state
}

func (f *fakeGoogleOAuth) ExchangeCode(_ context.Context, code, redirectURI string) (*model.GoogleOAuthResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges = append(f.exchanges, exchangeCall{Code: code, RedirectURI: redirectURI})
	if f.exchangeErr != nil {
		return nil, f.exchangeErr
	}
	res := *f.result
	return &res, nil
}

func (f *fakeGoogleOAuth) FetchIdentity(_ context.Context, accessToken model.GoogleAccessToken) (*model.GoogleIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identities = append(f.identities, accessToken)
	if f.identityErr != nil {
		return nil, f.identityErr
	}
	id := *f.identity
	return &id, nil
}

func (f *fakeGoogleOAuth) Revoke(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes = append(f.revokes, token)
	return f.revokeErr
}

func (f *fakeGoogleOAuth) revoked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revokes...)
}

type googleFixture struct {
	repo   *memory.Memory
	cipher *fakeCipher
	oauth  *fakeGoogleOAuth
	uc     *usecase.GoogleWorkspaceUseCase
	now    time.Time
}

func newGoogleFixture() *googleFixture {
	f := &googleFixture{
		repo:   memory.New(),
		cipher: &fakeCipher{},
		oauth:  newFakeGoogleOAuth(),
		now:    time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
	}
	f.uc = usecase.NewGoogleWorkspaceUseCase(f.oauth, usecase.NewGoogleWorkspaceAccess(f.repo, f.cipher, nil),
		usecase.GoogleWorkspaceConfig{BaseURL: "https://robin.example.com"})
	f.uc.SetNowForTest(func() time.Time { return f.now })
	return f
}

func (f *googleFixture) assertNothingStored(t *testing.T) {
	t.Helper()
	_, err := f.repo.GoogleWorkspaceCredential().Get(context.Background(), testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
}

func TestGoogleWorkspaceUseCase_AuthorizeURL(t *testing.T) {
	f := newGoogleFixture()

	got, err := f.uc.AuthorizeURL(context.Background(), testKey, "s1")
	gt.NoError(t, err).Required()
	gt.String(t, got).Equal("https://accounts.google.com/o/oauth2/v2/auth?state=s1")
	gt.Array(t, f.oauth.authorizes).Length(1).Required()
	gt.Value(t, f.oauth.authorizes[0]).Equal(authorizeCall{
		State:       "s1",
		RedirectURI: googleCallbackURL,
		Scopes: []string{
			"openid",
			"email",
			"https://www.googleapis.com/auth/calendar.readonly",
			"https://www.googleapis.com/auth/drive.readonly",
			"https://www.googleapis.com/auth/gmail.readonly",
		},
	})
}

func TestGoogleWorkspaceUseCase_HandleCallback(t *testing.T) {
	ctx := context.Background()
	f := newGoogleFixture()

	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()

	gt.Value(t, f.oauth.exchanges).Equal([]exchangeCall{{Code: "code-1", RedirectURI: googleCallbackURL}})
	gt.Value(t, f.oauth.identities).Equal([]model.GoogleAccessToken{"access-1"})
	gt.Array(t, f.oauth.revoked()).Length(0)

	cred, err := f.repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, cred.Key()).Equal(testKey)
	gt.Bool(t, bytes.HasSuffix(cred.RefreshToken.Ciphertext, []byte("refresh-1"))).True()
	gt.Value(t, cred.Scopes).Equal(googleScopes)
	gt.String(t, cred.Subject).Equal("1234567890")
	gt.String(t, cred.Email).Equal("alice@example.com")
	gt.Bool(t, cred.CreatedAt.Equal(f.now)).True()
	gt.Bool(t, cred.UpdatedAt.Equal(f.now)).True()
}

func TestGoogleWorkspaceUseCase_AuthorizeURLWhenConnected(t *testing.T) {
	ctx := context.Background()
	f := newGoogleFixture()
	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()

	_, err := f.uc.AuthorizeURL(ctx, testKey, "s2")
	gt.Error(t, err).Is(usecase.ErrGoogleWorkspaceAlreadyConnected)
	gt.Array(t, f.oauth.authorizes).Length(0)
}

var otherKey = model.UserKey{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}

func TestGoogleWorkspaceUseCase_HandleCallbackRejects(t *testing.T) {
	cases := map[string]struct {
		setup func(f *googleFixture)
		// connectedToOther connects the authorized Google account to
		// otherKey before the callback.
		connectedToOther bool
		wantErr          error
		wantRevoked      []string
		wantUserinfo     bool
	}{
		"gmail scope not granted": {
			setup: func(f *googleFixture) {
				f.oauth.result.Scopes = []string{
					"openid",
					"https://www.googleapis.com/auth/calendar.readonly",
					"https://www.googleapis.com/auth/drive.readonly",
				}
			},
			wantErr:      usecase.ErrGoogleScopeNotGranted,
			wantRevoked:  []string{"refresh-1"},
			wantUserinfo: true,
		},
		"no scope granted": {
			setup:        func(f *googleFixture) { f.oauth.result.Scopes = nil },
			wantErr:      usecase.ErrGoogleScopeNotGranted,
			wantRevoked:  []string{"refresh-1"},
			wantUserinfo: true,
		},
		"no refresh token": {
			setup:        func(f *googleFixture) { f.oauth.result.RefreshToken = "" },
			wantErr:      usecase.ErrGoogleConnectRejected,
			wantRevoked:  []string{"access-1"},
			wantUserinfo: true,
		},
		// The account cannot be identified, so it may be another user's
		// connection: nothing is revoked.
		"userinfo fails": {
			setup:        func(f *googleFixture) { f.oauth.identityErr = errors.New("userinfo unavailable") },
			wantUserinfo: true,
		},
		"no email": {
			setup:        func(f *googleFixture) { f.oauth.identity = &model.GoogleIdentity{Subject: "1234567890"} },
			wantErr:      usecase.ErrGoogleConnectRejected,
			wantUserinfo: true,
		},
		"no subject": {
			setup:        func(f *googleFixture) { f.oauth.identity = &model.GoogleIdentity{Email: "alice@example.com"} },
			wantErr:      usecase.ErrGoogleConnectRejected,
			wantUserinfo: true,
		},
		// Revoking would end the other user's connection of the same account,
		// even when a scope is also missing.
		"account connected to another user": {
			setup:            func(f *googleFixture) { f.oauth.result.Scopes = []string{"openid"} },
			connectedToOther: true,
			wantErr:          usecase.ErrGoogleAccountInUse,
			wantUserinfo:     true,
		},
		"encryption fails": {
			setup:        func(f *googleFixture) { f.cipher.encryptErr = errors.New("kms unavailable") },
			wantRevoked:  []string{"refresh-1"},
			wantUserinfo: true,
		},
		"code exchange fails": {
			setup: func(f *googleFixture) { f.oauth.exchangeErr = errors.New("invalid_grant") },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newGoogleFixture()
			if tc.connectedToOther {
				gt.NoError(t, usecase.NewGoogleWorkspaceAccess(f.repo, f.cipher, nil).
					Store(context.Background(), otherKey, "refresh-other", googleScopes, aliceIdentity, f.now)).Required()
			}
			tc.setup(f)

			err := f.uc.HandleCallback(context.Background(), testKey, "code-1")
			gt.Value(t, err).NotNil().Required()
			if tc.wantErr != nil {
				gt.Error(t, err).Is(tc.wantErr)
			}
			gt.Value(t, f.oauth.revoked()).Equal(tc.wantRevoked)
			gt.Value(t, len(f.oauth.identities) > 0).Equal(tc.wantUserinfo)
			f.assertNothingStored(t)
		})
	}
}

func TestGoogleWorkspaceUseCase_HandleCallbackRevokeFailureKeepsCause(t *testing.T) {
	f := newGoogleFixture()
	f.oauth.result.Scopes = []string{"openid"}
	f.oauth.revokeErr = errors.New("google unavailable")

	err := f.uc.HandleCallback(context.Background(), testKey, "code-1")
	gt.Error(t, err).Is(usecase.ErrGoogleScopeNotGranted)
	gt.Value(t, f.oauth.revoked()).Equal([]string{"refresh-1"})
	f.assertNothingStored(t)
}

// A second connection by a connected user changes nothing: the code is not
// exchanged, nothing is revoked, and the stored credential stays.
func TestGoogleWorkspaceUseCase_CallbackWhenConnectedIsIgnored(t *testing.T) {
	ctx := context.Background()
	f := newGoogleFixture()
	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()

	f.oauth.result.RefreshToken = "refresh-2"
	f.oauth.identity = bobIdentity
	err := f.uc.HandleCallback(ctx, testKey, "code-2")
	gt.Error(t, err).Is(usecase.ErrGoogleWorkspaceAlreadyConnected)

	gt.Value(t, f.oauth.exchanges).Equal([]exchangeCall{{Code: "code-1", RedirectURI: googleCallbackURL}})
	gt.Array(t, f.oauth.revoked()).Length(0)
	cred, err := f.repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, bytes.HasSuffix(cred.RefreshToken.Ciphertext, []byte("refresh-1"))).True()
	gt.String(t, cred.Email).Equal("alice@example.com")
}

// racingRepository reports every Google account as free, as a check made just
// before another request connects it would, so the conflict is found only
// when the credential is created.
type racingRepository struct {
	interfaces.Repository
}

func (r racingRepository) GoogleWorkspaceCredential() interfaces.GoogleWorkspaceCredentialRepository {
	return racingCredentials{r.Repository.GoogleWorkspaceCredential()}
}

type racingCredentials struct {
	interfaces.GoogleWorkspaceCredentialRepository
}

func (racingCredentials) AccountInUse(context.Context, model.UserKey, string) (bool, error) {
	return false, nil
}

func TestGoogleWorkspaceUseCase_ConflictFoundWhenStoring(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	cipher := &fakeCipher{}
	oauth := newFakeGoogleOAuth()
	uc := usecase.NewGoogleWorkspaceUseCase(oauth, usecase.NewGoogleWorkspaceAccess(racingRepository{repo}, cipher, nil),
		usecase.GoogleWorkspaceConfig{BaseURL: "https://robin.example.com"})
	gt.NoError(t, usecase.NewGoogleWorkspaceAccess(repo, cipher, nil).
		Store(ctx, otherKey, "refresh-other", googleScopes, aliceIdentity, time.Now())).Required()

	err := uc.HandleCallback(ctx, testKey, "code-1")
	gt.Error(t, err).Is(usecase.ErrGoogleAccountInUse)
	gt.Array(t, oauth.revoked()).Length(0)
	_, err = repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
}

func TestGoogleWorkspaceUseCase_ReconnectAfterDisconnect(t *testing.T) {
	ctx := context.Background()
	f := newGoogleFixture()
	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()
	gt.NoError(t, f.uc.Disconnect(ctx, testKey)).Required()

	f.oauth.result.RefreshToken = "refresh-2"
	f.oauth.identity = bobIdentity
	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-2")).Required()

	cred, err := f.repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, bytes.HasSuffix(cred.RefreshToken.Ciphertext, []byte("refresh-2"))).True()
	gt.String(t, cred.Email).Equal("bob@example.com")

	// The first account is free again for another user.
	inUse, err := usecase.NewGoogleWorkspaceAccess(f.repo, f.cipher, nil).AccountInUse(ctx, otherKey, aliceIdentity.Subject)
	gt.NoError(t, err).Required()
	gt.Bool(t, inUse).False()
}

func TestGoogleWorkspaceUseCase_Status(t *testing.T) {
	ctx := context.Background()
	f := newGoogleFixture()

	status, err := f.uc.Status(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GoogleWorkspaceStatus{Connected: false, Email: ""})

	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()
	status, err = f.uc.Status(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GoogleWorkspaceStatus{Connected: true, Email: "alice@example.com"})

	// Status reads only Firestore: one exchange and one userinfo call came
	// from HandleCallback, none from Status.
	gt.Array(t, f.oauth.exchanges).Length(1)
	gt.Array(t, f.oauth.identities).Length(1)
	gt.Number(t, f.cipher.decryptCount()).Equal(0)
}

func TestGoogleWorkspaceUseCase_Disconnect(t *testing.T) {
	cases := map[string]struct {
		revokeErr  error
		wantErr    bool
		wantStored bool
	}{
		"revoked":               {},
		"token already invalid": {revokeErr: goerr.Wrap(interfaces.ErrGoogleTokenInvalid, "rejected")},
		"google unavailable":    {revokeErr: errors.New("503"), wantErr: true, wantStored: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newGoogleFixture()
			gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()
			f.oauth.revokeErr = tc.revokeErr

			err := f.uc.Disconnect(ctx, testKey)
			if tc.wantErr {
				gt.Value(t, err).NotNil()
			} else {
				gt.NoError(t, err)
			}
			gt.Value(t, f.oauth.revoked()).Equal([]string{"refresh-1"})

			_, getErr := f.repo.GoogleWorkspaceCredential().Get(ctx, testKey)
			if tc.wantStored {
				gt.NoError(t, getErr)
			} else {
				gt.Error(t, getErr).Is(interfaces.ErrNotFound)
			}
		})
	}
}

func TestGoogleWorkspaceUseCase_DisconnectNotConnected(t *testing.T) {
	f := newGoogleFixture()

	gt.NoError(t, f.uc.Disconnect(context.Background(), testKey))
	gt.Array(t, f.oauth.revoked()).Length(0)
}

func TestGoogleWorkspaceUseCase_DisconnectDecryptError(t *testing.T) {
	ctx := context.Background()
	f := newGoogleFixture()
	gt.NoError(t, f.uc.HandleCallback(ctx, testKey, "code-1")).Required()
	f.cipher.decryptErr = errors.New("key version disabled")

	gt.Value(t, f.uc.Disconnect(ctx, testKey)).NotNil()
	gt.Array(t, f.oauth.revoked()).Length(0)
	_, err := f.repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err)
}
