package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/kms"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
)

const testKeyName = "projects/p/locations/l/keyRings/r/cryptoKeys/k"

// fakeCipher embeds the AAD into the ciphertext and refuses to decrypt with a
// different AAD, which is the property Cloud KMS provides.
type fakeCipher struct {
	mu          sync.Mutex
	encryptErr  error
	decryptErr  error
	encryptions []cipherCall
	decryptions []cipherCall
}

type cipherCall struct {
	Data []byte
	AAD  []byte
}

func (c *fakeCipher) Encrypt(_ context.Context, plaintext, aad []byte) (*model.EncryptedData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.encryptions = append(c.encryptions, cipherCall{Data: bytes.Clone(plaintext), AAD: bytes.Clone(aad)})
	if c.encryptErr != nil {
		return nil, c.encryptErr
	}
	ciphertext := append(append(bytes.Clone(aad), '|'), plaintext...)
	return &model.EncryptedData{KeyName: testKeyName, Ciphertext: ciphertext}, nil
}

func (c *fakeCipher) Decrypt(_ context.Context, data *model.EncryptedData, aad []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decryptions = append(c.decryptions, cipherCall{Data: bytes.Clone(data.Ciphertext), AAD: bytes.Clone(aad)})
	if c.decryptErr != nil {
		return nil, c.decryptErr
	}
	prefix := append(bytes.Clone(aad), '|')
	if !bytes.HasPrefix(data.Ciphertext, prefix) {
		return nil, errors.New("aad mismatch")
	}
	return bytes.TrimPrefix(data.Ciphertext, prefix), nil
}

func (c *fakeCipher) decryptCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.decryptions)
}

// fakeUserClientFactory answers auth.test per token.
type fakeUserClientFactory struct {
	mu         sync.Mutex
	identities map[model.SlackUserToken]*model.SlackIdentity
	errs       map[model.SlackUserToken]error
	tokens     []model.SlackUserToken
	authTests  int
}

func newFakeUserClientFactory() *fakeUserClientFactory {
	return &fakeUserClientFactory{
		identities: make(map[model.SlackUserToken]*model.SlackIdentity),
		errs:       make(map[model.SlackUserToken]error),
	}
}

func (f *fakeUserClientFactory) New(token model.SlackUserToken) interfaces.SlackUserClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token)
	return &fakeUserClient{factory: f, token: token}
}

func (f *fakeUserClientFactory) authTestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authTests
}

type fakeUserClient struct {
	factory *fakeUserClientFactory
	token   model.SlackUserToken
}

func (c *fakeUserClient) AuthTest(_ context.Context) (*model.SlackIdentity, error) {
	c.factory.mu.Lock()
	defer c.factory.mu.Unlock()
	c.factory.authTests++
	if err, ok := c.factory.errs[c.token]; ok {
		return nil, err
	}
	id, ok := c.factory.identities[c.token]
	if !ok {
		return nil, goerr.Wrap(interfaces.ErrSlackTokenInvalid, "unknown token")
	}
	return id, nil
}

// SearchMessages is not used by the tests of this package; the agents test
// the search with their own client.
func (c *fakeUserClient) SearchMessages(context.Context, string, int) ([]model.SlackMessageHit, error) {
	return nil, errors.New("search is not used in usecase tests")
}

var testKey = model.UserKey{TeamID: "T0123ABCD", UserID: "U0123ABCD"}

func TestSlackUserAccess_StoreAndClient(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	cipher := &fakeCipher{}
	factory := newFakeUserClientFactory()
	access := usecase.NewSlackUserAccess(repo, cipher, factory)

	now := time.Now().UTC()
	gt.NoError(t, access.Store(ctx, testKey, "xoxp-secret", []string{"search:read"}, now)).Required()

	cred, err := repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.String(t, cred.AccessToken.KeyName).Equal(testKeyName)
	gt.Bool(t, bytes.Contains(cred.AccessToken.Ciphertext, []byte("xoxp-secret"))).True()
	gt.Value(t, cred.Scopes).Equal([]string{"search:read"})
	gt.Bool(t, cred.CreatedAt.Equal(now)).True()
	gt.Bool(t, cred.UpdatedAt.Equal(now)).True()
	gt.Array(t, cipher.encryptions).Length(1).Required()
	gt.Value(t, cipher.encryptions[0].AAD).Equal(usecase.TokenAADForTest(testKey))
	gt.String(t, string(cipher.encryptions[0].AAD)).Equal("robin:slack-user-token:v1:T0123ABCD:U0123ABCD")

	_, err = access.Client(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Array(t, cipher.decryptions).Length(1).Required()
	gt.Value(t, cipher.decryptions[0].Data).Equal(cred.AccessToken.Ciphertext)
	gt.Value(t, cipher.decryptions[0].AAD).Equal(usecase.TokenAADForTest(testKey))
	gt.Array(t, factory.tokens).Length(1).Required()
	gt.Value(t, factory.tokens[0]).Equal(model.SlackUserToken("xoxp-secret"))
}

func TestSlackUserAccess_StoreKeepsCreatedAt(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewSlackUserAccess(repo, &fakeCipher{}, newFakeUserClientFactory())

	first := time.Now().Add(-time.Hour).UTC()
	second := time.Now().UTC()
	gt.NoError(t, access.Store(ctx, testKey, "xoxp-first", []string{"search:read"}, first)).Required()
	gt.NoError(t, access.Store(ctx, testKey, "xoxp-second", []string{"search:read"}, second)).Required()

	cred, err := repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, cred.CreatedAt.Equal(first)).True()
	gt.Bool(t, cred.UpdatedAt.Equal(second)).True()
	gt.Bool(t, bytes.Contains(cred.AccessToken.Ciphertext, []byte("xoxp-second"))).True()
}

func TestSlackUserAccess_StoreEncryptError(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewSlackUserAccess(repo, &fakeCipher{encryptErr: errors.New("kms unavailable")}, newFakeUserClientFactory())

	gt.Value(t, access.Store(ctx, testKey, "xoxp-secret", []string{"search:read"}, time.Now())).NotNil()
	_, err := repo.SlackCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
}

func TestSlackUserAccess_ClientNotConnected(t *testing.T) {
	cipher := &fakeCipher{}
	access := usecase.NewSlackUserAccess(memory.New(), cipher, newFakeUserClientFactory())

	_, err := access.Client(context.Background(), testKey)
	gt.Error(t, err).Is(usecase.ErrSlackNotConnected)
	gt.Number(t, cipher.decryptCount()).Equal(0)
}

func TestSlackUserAccess_ClientDecryptError(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	cipher := &fakeCipher{}
	access := usecase.NewSlackUserAccess(repo, cipher, newFakeUserClientFactory())
	gt.NoError(t, access.Store(ctx, testKey, "xoxp-secret", []string{"search:read"}, time.Now())).Required()

	cipher.decryptErr = errors.New("permission denied")
	_, err := access.Client(ctx, testKey)
	gt.Value(t, err).NotNil().Required()
	gt.Bool(t, errors.Is(err, usecase.ErrSlackNotConnected)).False()
}

func TestSlackUserAccess_CiphertextOfAnotherUserCannotBeDecrypted(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	cipher := &fakeCipher{}
	access := usecase.NewSlackUserAccess(repo, cipher, newFakeUserClientFactory())
	gt.NoError(t, access.Store(ctx, testKey, "xoxp-owner", []string{"search:read"}, time.Now())).Required()

	ownerCred, err := repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	other := model.UserKey{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}
	copied := *ownerCred
	copied.UserID = other.UserID
	gt.NoError(t, repo.SlackCredential().Put(ctx, other, &copied)).Required()

	_, err = access.Client(ctx, other)
	gt.Value(t, err).NotNil()
}

// TestSlackUserAccess_WithCloudKMS stores and reads a token through a real
// Cloud KMS key named by TEST_GCP_KMS.
func TestSlackUserAccess_WithCloudKMS(t *testing.T) {
	keyName := os.Getenv("TEST_GCP_KMS")
	if keyName == "" {
		t.Skip("TEST_GCP_KMS is not set")
	}
	ctx := context.Background()
	cipher, err := kms.New(ctx, keyName)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { gt.NoError(t, cipher.Close()) })

	repo := memory.New()
	factory := newFakeUserClientFactory()
	access := usecase.NewSlackUserAccess(repo, cipher, factory)
	gt.NoError(t, access.Store(ctx, testKey, "xoxp-kms-token", []string{"search:read"}, time.Now())).Required()

	cred, err := repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.String(t, cred.AccessToken.KeyName).Equal(keyName)
	gt.Bool(t, bytes.Contains(cred.AccessToken.Ciphertext, []byte("xoxp-kms-token"))).False()

	_, err = access.Client(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Array(t, factory.tokens).Length(1).Required()
	gt.Value(t, factory.tokens[0]).Equal(model.SlackUserToken("xoxp-kms-token"))

	other := model.UserKey{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}
	copied := *cred
	copied.UserID = other.UserID
	gt.NoError(t, repo.SlackCredential().Put(ctx, other, &copied)).Required()
	_, err = access.Client(ctx, other)
	gt.Value(t, err).NotNil()
}

func TestSlackUserAccess_DisconnectAndConnected(t *testing.T) {
	ctx := context.Background()
	access := usecase.NewSlackUserAccess(memory.New(), &fakeCipher{}, newFakeUserClientFactory())

	connected, err := access.Connected(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, connected).False()

	gt.NoError(t, access.Store(ctx, testKey, "xoxp-secret", []string{"search:read"}, time.Now())).Required()
	connected, err = access.Connected(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, connected).True()

	client, err := access.Client(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.NoError(t, access.Disconnect(ctx, client)).Required()
	connected, err = access.Connected(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, connected).False()
}

// A mention that read the old token must not delete the token a sign-in
// stored after that read.
func TestSlackUserAccess_DisconnectKeepsNewerToken(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	access := usecase.NewSlackUserAccess(repo, &fakeCipher{}, newFakeUserClientFactory())

	gt.NoError(t, access.Store(ctx, testKey, "xoxp-old", []string{"search:read"}, time.Now())).Required()
	oldClient, err := access.Client(ctx, testKey)
	gt.NoError(t, err).Required()

	gt.NoError(t, access.Store(ctx, testKey, "xoxp-new", []string{"search:read"}, time.Now())).Required()
	gt.NoError(t, access.Disconnect(ctx, oldClient)).Required()

	cred, err := repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()
	gt.Bool(t, bytes.HasSuffix(cred.AccessToken.Ciphertext, []byte("xoxp-new"))).True()
}
