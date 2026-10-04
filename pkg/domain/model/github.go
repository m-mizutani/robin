package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// GitHubUserID is the numeric ID of a GitHub account. Unlike the login, it
// never changes.
type GitHubUserID int64

// GitHubAccessToken (ghu_...) and GitHubRefreshToken (ghr_...) are plaintext
// user tokens of the GitHub App. They are never persisted and never logged;
// only their KMS ciphertexts are stored.
type GitHubAccessToken string
type GitHubRefreshToken string

// GitHubToken is what GitHub's token endpoint returns. Either all three
// expiring fields are set, or none is: an app with token expiration turned
// off returns an access token that does not expire and no refresh token.
type GitHubToken struct {
	AccessToken           GitHubAccessToken `masq:"secret"`
	AccessTokenExpiresAt  time.Time
	RefreshToken          GitHubRefreshToken `masq:"secret"`
	RefreshTokenExpiresAt time.Time
}

func (t *GitHubToken) Validate() error {
	if t.AccessToken == "" {
		return goerr.New("empty github access token")
	}
	expiring := !t.AccessTokenExpiresAt.IsZero()
	if expiring != (t.RefreshToken != "") || expiring != !t.RefreshTokenExpiresAt.IsZero() {
		return goerr.New("github token has only part of the expiration fields",
			goerr.V("has_access_token_expiry", expiring),
			goerr.V("has_refresh_token", t.RefreshToken != ""),
			goerr.V("has_refresh_token_expiry", !t.RefreshTokenExpiresAt.IsZero()))
	}
	return nil
}

// GitHubIdentity is the GitHub account a token belongs to, as reported by
// GET /user.
type GitHubIdentity struct {
	ID    GitHubUserID
	Login string
}

func (x *GitHubIdentity) Validate() error {
	if x.ID <= 0 {
		return goerr.New("invalid github user ID", goerr.V("github_user_id", x.ID))
	}
	if x.Login == "" {
		return goerr.New("empty github login", goerr.V("github_user_id", x.ID))
	}
	return nil
}

// GitHubCredential is one user's connection to a GitHub account: both user
// tokens as KMS ciphertexts, their expiry, and the lease that lets only one
// instance refresh them at a time. A refresh token can be used only once, so
// two instances refreshing together would leave one of them with an invalid
// token.
type GitHubCredential struct {
	TeamID SlackTeamID
	UserID SlackUserID
	// ConnectionID is issued on every connection and kept on refresh, so a
	// deletion never removes a newer connection.
	ConnectionID         string
	GitHubUserID         GitHubUserID
	GitHubLogin          string
	AccessToken          EncryptedData
	AccessTokenExpiresAt time.Time // zero: the token does not expire
	// RefreshToken is nil when the tokens do not expire.
	RefreshToken          *EncryptedData
	RefreshTokenExpiresAt time.Time
	RefreshLeaseID        string // empty: no instance is refreshing
	RefreshLeaseExpiresAt time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (c *GitHubCredential) Key() UserKey {
	return UserKey{TeamID: c.TeamID, UserID: c.UserID}
}

func (c *GitHubCredential) Validate() error {
	if err := c.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid github credential key")
	}
	if c.ConnectionID == "" {
		return goerr.New("empty github credential connection ID")
	}
	if c.GitHubUserID <= 0 {
		return goerr.New("invalid github credential user ID", goerr.V("github_user_id", c.GitHubUserID))
	}
	if c.GitHubLogin == "" {
		return goerr.New("empty github credential login")
	}
	if err := c.AccessToken.Validate(); err != nil {
		return goerr.Wrap(err, "invalid github credential access token")
	}
	if c.RefreshToken != nil {
		if err := c.RefreshToken.Validate(); err != nil {
			return goerr.Wrap(err, "invalid github credential refresh token")
		}
		if c.AccessTokenExpiresAt.IsZero() || c.RefreshTokenExpiresAt.IsZero() {
			return goerr.New("github credential with a refresh token has no expiry")
		}
	} else if !c.AccessTokenExpiresAt.IsZero() || !c.RefreshTokenExpiresAt.IsZero() {
		return goerr.New("github credential without a refresh token has an expiry")
	}
	if (c.RefreshLeaseID == "") != c.RefreshLeaseExpiresAt.IsZero() {
		return goerr.New("github credential has only part of the refresh lease")
	}
	if c.CreatedAt.IsZero() {
		return goerr.New("empty github credential created_at")
	}
	if c.UpdatedAt.IsZero() {
		return goerr.New("empty github credential updated_at")
	}
	return nil
}

// NeedsRefresh reports whether the access token expires within margin.
func (c *GitHubCredential) NeedsRefresh(now time.Time, margin time.Duration) bool {
	return !c.AccessTokenExpiresAt.IsZero() && !now.Add(margin).Before(c.AccessTokenExpiresAt)
}

// RefreshExpired reports whether the refresh token can no longer be used, so
// the connection is lost until the user connects again.
func (c *GitHubCredential) RefreshExpired(now time.Time) bool {
	return c.RefreshToken != nil && !now.Before(c.RefreshTokenExpiresAt)
}

// LeaseHeld reports whether an instance holds an unexpired refresh lease.
func (c *GitHubCredential) LeaseHeld(now time.Time) bool {
	return c.RefreshLeaseID != "" && now.Before(c.RefreshLeaseExpiresAt)
}

type GitHubIssueSummary struct {
	Repository    string // owner/repo
	Number        int
	Title         string
	State         string
	IsPullRequest bool
	Author        string
	UpdatedAt     time.Time
	HTMLURL       string
}

type GitHubCodeHit struct {
	Repository string
	Path       string
	HTMLURL    string
}

type GitHubComment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

type GitHubIssue struct {
	GitHubIssueSummary
	Body     string
	Labels   []string
	Comments []GitHubComment // the first page of comments
}

// GitHubContent is a file (Text) or a directory (Entries).
type GitHubContent struct {
	Type    string // "file" or "dir"
	Path    string
	Text    string
	Entries []string
	HTMLURL string
}

// GitHubAccount records which user a GitHub account is connected to, so that
// one GitHub account is never connected to two users: GitHub deletes an app
// authorization per account, and one user's disconnection would end the other
// user's access.
type GitHubAccount struct {
	GitHubUserID GitHubUserID
	TeamID       SlackTeamID
	UserID       SlackUserID
	CreatedAt    time.Time
}

func (a *GitHubAccount) Key() UserKey {
	return UserKey{TeamID: a.TeamID, UserID: a.UserID}
}
