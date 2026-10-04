package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/kms"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
)

var (
	googleScopes = []string{
		"openid",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/calendar.readonly",
		"https://www.googleapis.com/auth/drive.readonly",
		"https://www.googleapis.com/auth/gmail.readonly",
	}
	aliceIdentity = &model.GoogleIdentity{Subject: "1234567890", Email: "alice@example.com"}
)

func TestGoogleWorkspaceAccess_StoreAndToken(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	cipher := &fakeCipher{}
	access := usecase.NewGoogleWorkspaceAccess(repo, cipher, nil)

	now := time.Now().UTC()
	gt.NoError(t, access.Store(ctx, testKey, "refresh-secret", googleScopes, aliceIdentity, now)).Required()

	cred, err := repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.String(t, cred.RefreshToken.KeyName).Equal(testKeyName)
	gt.Bool(t, bytes.HasSuffix(cred.RefreshToken.Ciphertext, []byte("refresh-secret"))).True()
	gt.Value(t, cred.Scopes).Equal(googleScopes)
	gt.String(t, cred.Subject).Equal("1234567890")
	gt.String(t, cred.Email).Equal("alice@example.com")
	gt.Bool(t, cred.CreatedAt.Equal(now)).True()
	gt.Bool(t, cred.UpdatedAt.Equal(now)).True()
	gt.Array(t, cipher.encryptions).Length(1).Required()
	gt.Value(t, cipher.encryptions[0].AAD).Equal(usecase.GoogleTokenAADForTest(testKey))
	gt.String(t, string(cipher.encryptions[0].AAD)).Equal("robin:google-refresh-token:v1:T0123ABCD:U0123ABCD")

	token, err := access.Token(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, token.RefreshToken).Equal(model.GoogleRefreshToken("refresh-secret"))
	gt.Array(t, cipher.decryptions).Length(1).Required()
	gt.Value(t, cipher.decryptions[0].AAD).Equal(usecase.GoogleTokenAADForTest(testKey))
}

// fakeGoogleClients records the refresh token each client was built with and
// answers every read with the configured values.
type fakeGoogleClients struct {
	tokens    []model.GoogleRefreshToken
	err       error
	gmail     []model.GmailMessageSummary
	gmailQ    []model.GmailSearchQuery
	message   *model.GmailMessage
	drive     []model.DriveFile
	driveQ    []model.DriveSearchQuery
	file      *model.DriveFileText
	events    []model.CalendarEvent
	calendarQ []model.CalendarEventQuery
	ids       []string
}

func (f *fakeGoogleClients) New(token model.GoogleRefreshToken) interfaces.GoogleWorkspaceClient {
	f.tokens = append(f.tokens, token)
	return &fakeGoogleClient{f: f}
}

type fakeGoogleClient struct{ f *fakeGoogleClients }

func (c *fakeGoogleClient) SearchGmail(_ context.Context, q model.GmailSearchQuery) ([]model.GmailMessageSummary, error) {
	c.f.gmailQ = append(c.f.gmailQ, q)
	return c.f.gmail, c.f.err
}

func (c *fakeGoogleClient) GetGmailMessage(_ context.Context, id string) (*model.GmailMessage, error) {
	c.f.ids = append(c.f.ids, id)
	return c.f.message, c.f.err
}

func (c *fakeGoogleClient) SearchDrive(_ context.Context, q model.DriveSearchQuery) ([]model.DriveFile, error) {
	c.f.driveQ = append(c.f.driveQ, q)
	return c.f.drive, c.f.err
}

func (c *fakeGoogleClient) GetDriveFileText(_ context.Context, id string) (*model.DriveFileText, error) {
	c.f.ids = append(c.f.ids, id)
	return c.f.file, c.f.err
}

func (c *fakeGoogleClient) ListCalendarEvents(_ context.Context, q model.CalendarEventQuery) ([]model.CalendarEvent, error) {
	c.f.calendarQ = append(c.f.calendarQ, q)
	return c.f.events, c.f.err
}

func TestGoogleWorkspaceAccess_Read(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	cipher := &fakeCipher{}
	clients := &fakeGoogleClients{gmail: []model.GmailMessageSummary{{ID: "m1", Subject: "Plan"}}}
	access := usecase.NewGoogleWorkspaceAccess(repo, cipher, clients)
	gt.NoError(t, access.Store(ctx, testKey, "refresh-secret", googleScopes, aliceIdentity, time.Now())).Required()

	got, err := access.SearchGmail(ctx, testKey, model.GmailSearchQuery{Query: "plan", MaxResults: 10})
	gt.NoError(t, err).Required()
	gt.Equal(t, got, clients.gmail)
	gt.Equal(t, clients.tokens, []model.GoogleRefreshToken{"refresh-secret"})
	gt.Equal(t, clients.gmailQ, []model.GmailSearchQuery{{Query: "plan", MaxResults: 10}})
	gt.Value(t, cipher.decryptions[len(cipher.decryptions)-1].AAD).Equal(usecase.GoogleTokenAADForTest(testKey))
}

func TestGoogleWorkspaceAccess_ReadNotConnected(t *testing.T) {
	clients := &fakeGoogleClients{}
	access := usecase.NewGoogleWorkspaceAccess(memory.New(), &fakeCipher{}, clients)
	_, err := access.SearchDrive(context.Background(), testKey, model.DriveSearchQuery{Query: "x", MaxResults: 1})
	gt.Error(t, err).Is(usecase.ErrGoogleWorkspaceNotConnected)
	gt.Array(t, clients.tokens).Length(0)
}

func TestGoogleWorkspaceAccess_ReadRejectedToken(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	clients := &fakeGoogleClients{err: adapterError(interfaces.ErrGoogleTokenInvalid)}
	access := usecase.NewGoogleWorkspaceAccess(repo, &fakeCipher{}, clients)
	gt.NoError(t, access.Store(ctx, testKey, "refresh-secret", googleScopes, aliceIdentity, time.Now())).Required()

	_, err := access.GetGmailMessage(ctx, testKey, "m1")
	gt.Error(t, err).Is(usecase.ErrGoogleWorkspaceReconnectRequired)

	// The credential is kept: the user reconnects from the settings page.
	_, err = repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err)

	clients.err = adapterError(interfaces.ErrGoogleNotFound)
	_, err = access.GetDriveFileText(ctx, testKey, "f1")
	gt.Error(t, err).Is(interfaces.ErrGoogleNotFound)
	gt.False(t, errors.Is(err, usecase.ErrGoogleWorkspaceReconnectRequired))
}

// adapterError wraps err the way an adapter does, so tests check that
// callers discriminate it with errors.Is.
func adapterError(err error) error {
	return errors.Join(errors.New("adapter failed"), err)
}

var bobIdentity = &model.GoogleIdentity{Subject: "999", Email: "bob@example.com"}

func TestGoogleWorkspaceAccess_StoreRefusesSecondCredential(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewGoogleWorkspaceAccess(repo, &fakeCipher{}, nil)

	gt.NoError(t, access.Store(ctx, testKey, "refresh-first", googleScopes, aliceIdentity, time.Now())).Required()
	err := access.Store(ctx, testKey, "refresh-second", googleScopes, bobIdentity, time.Now())
	gt.Error(t, err).Is(interfaces.ErrAlreadyExists)

	cred, err := repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, bytes.HasSuffix(cred.RefreshToken.Ciphertext, []byte("refresh-first"))).True()
	gt.String(t, cred.Email).Equal("alice@example.com")
}

func TestGoogleWorkspaceAccess_AccountOfAnotherUser(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewGoogleWorkspaceAccess(repo, &fakeCipher{}, nil)
	other := model.UserKey{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}
	gt.NoError(t, access.Store(ctx, other, "refresh-other", googleScopes, aliceIdentity, time.Now())).Required()

	inUse, err := access.AccountInUse(ctx, testKey, aliceIdentity.Subject)
	gt.NoError(t, err).Required()
	gt.Bool(t, inUse).True()

	err = access.Store(ctx, testKey, "refresh-mine", googleScopes, aliceIdentity, time.Now())
	gt.Error(t, err).Is(interfaces.ErrGoogleAccountInUse)
	_, err = repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
}

func TestGoogleWorkspaceAccess_StoreEncryptError(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewGoogleWorkspaceAccess(repo, &fakeCipher{encryptErr: errors.New("kms unavailable")}, nil)

	gt.Value(t, access.Store(ctx, testKey, "refresh-secret", googleScopes, aliceIdentity, time.Now())).NotNil()
	_, err := repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
}

func TestGoogleWorkspaceAccess_TokenNotConnected(t *testing.T) {
	cipher := &fakeCipher{}
	access := usecase.NewGoogleWorkspaceAccess(memory.New(), cipher, nil)

	_, err := access.Token(context.Background(), testKey)
	gt.Error(t, err).Is(usecase.ErrGoogleWorkspaceNotConnected)
	gt.Number(t, cipher.decryptCount()).Equal(0)
}

func TestGoogleWorkspaceAccess_TokenDecryptError(t *testing.T) {
	ctx := context.Background()
	cipher := &fakeCipher{}
	access := usecase.NewGoogleWorkspaceAccess(memory.New(), cipher, nil)
	gt.NoError(t, access.Store(ctx, testKey, "refresh-secret", googleScopes, aliceIdentity, time.Now())).Required()

	cipher.decryptErr = errors.New("permission denied")
	_, err := access.Token(ctx, testKey)
	gt.Value(t, err).NotNil().Required()
	gt.Bool(t, errors.Is(err, usecase.ErrGoogleWorkspaceNotConnected)).False()
}

func TestGoogleWorkspaceAccess_CiphertextOfAnotherUserCannotBeDecrypted(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewGoogleWorkspaceAccess(repo, &fakeCipher{}, nil)
	gt.NoError(t, access.Store(ctx, testKey, "refresh-owner", googleScopes, aliceIdentity, time.Now())).Required()

	ownerCred, err := repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	other := model.UserKey{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}
	copied := *ownerCred
	copied.UserID = other.UserID
	copied.Subject = "another-google-account"
	gt.NoError(t, repo.GoogleWorkspaceCredential().Create(ctx, other, &copied)).Required()

	_, err = access.Token(ctx, other)
	gt.Value(t, err).NotNil()
}

// A disconnection that read the old token and arrives after the user
// disconnected and connected again must not delete the new credential.
func TestGoogleWorkspaceAccess_DeleteKeepsNewerCredential(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewGoogleWorkspaceAccess(repo, &fakeCipher{}, nil)

	gt.NoError(t, access.Store(ctx, testKey, "refresh-old", googleScopes, aliceIdentity, time.Now())).Required()
	oldToken, err := access.Token(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.NoError(t, access.Delete(ctx, oldToken)).Required()

	gt.NoError(t, access.Store(ctx, testKey, "refresh-new", googleScopes, aliceIdentity, time.Now())).Required()
	gt.NoError(t, access.Delete(ctx, oldToken)).Required()

	cred, err := repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, bytes.HasSuffix(cred.RefreshToken.Ciphertext, []byte("refresh-new"))).True()
}

func TestGoogleWorkspaceAccess_DeleteAndConnection(t *testing.T) {
	ctx := context.Background()
	access := usecase.NewGoogleWorkspaceAccess(memory.New(), &fakeCipher{}, nil)

	status, err := access.Connection(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GoogleWorkspaceStatus{})

	gt.NoError(t, access.Store(ctx, testKey, "refresh-secret", googleScopes, aliceIdentity, time.Now())).Required()
	status, err = access.Connection(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GoogleWorkspaceStatus{Connected: true, Email: "alice@example.com"})

	token, err := access.Token(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.NoError(t, access.Delete(ctx, token)).Required()
	status, err = access.Connection(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, status).Equal(&usecase.GoogleWorkspaceStatus{})
}

// TestGoogleWorkspaceAccess_WithCloudKMS stores and reads a refresh token
// through a real Cloud KMS key named by TEST_GCP_KMS.
func TestGoogleWorkspaceAccess_WithCloudKMS(t *testing.T) {
	keyName := os.Getenv("TEST_GCP_KMS")
	if keyName == "" {
		t.Skip("TEST_GCP_KMS is not set")
	}
	ctx := context.Background()
	cipher, err := kms.New(ctx, keyName)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { gt.NoError(t, cipher.Close()) })

	repo := memory.New()
	access := usecase.NewGoogleWorkspaceAccess(repo, cipher, nil)
	gt.NoError(t, access.Store(ctx, testKey, "refresh-kms-token", googleScopes, aliceIdentity, time.Now())).Required()

	cred, err := repo.GoogleWorkspaceCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.String(t, cred.RefreshToken.KeyName).Equal(keyName)
	gt.Bool(t, bytes.Contains(cred.RefreshToken.Ciphertext, []byte("refresh-kms-token"))).False()

	token, err := access.Token(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Value(t, token.RefreshToken).Equal(model.GoogleRefreshToken("refresh-kms-token"))

	other := model.UserKey{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}
	copied := *cred
	copied.UserID = other.UserID
	copied.Subject = "another-google-account"
	gt.NoError(t, repo.GoogleWorkspaceCredential().Create(ctx, other, &copied)).Required()
	_, err = access.Token(ctx, other)
	gt.Value(t, err).NotNil()
}
