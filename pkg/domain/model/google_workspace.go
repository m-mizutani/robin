package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// GoogleRefreshToken is a plaintext Google refresh token. It is never
// persisted and never logged; only its KMS ciphertext is stored.
type GoogleRefreshToken string

// GoogleAccessToken is a plaintext Google access token. It is never persisted
// and never logged.
type GoogleAccessToken string

// GoogleOAuthResult is what Google's token endpoint returns for an
// authorization code.
type GoogleOAuthResult struct {
	AccessToken GoogleAccessToken `masq:"secret"`
	// RefreshToken is empty when Google returned none.
	RefreshToken GoogleRefreshToken `masq:"secret"`
	Scopes       []string
}

// GoogleIdentity is the Google account a token belongs to, as reported by the
// OpenID Connect userinfo endpoint.
type GoogleIdentity struct {
	// Subject is the "sub" claim: unique among Google accounts and never
	// reused.
	Subject string
	Email   string
}

// GoogleWorkspaceCredential holds one user's Google refresh token as KMS
// ciphertext, the scopes Google granted with it, and the Google account it
// belongs to.
type GoogleWorkspaceCredential struct {
	TeamID       SlackTeamID
	UserID       SlackUserID
	RefreshToken EncryptedData
	Scopes       []string
	Subject      string
	Email        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GoogleWorkspaceAccount records which user a Google account is connected to,
// so that one Google account is never connected to two users: Google revokes
// a grant per Google account and project, and one user's disconnection would
// end the other user's access.
type GoogleWorkspaceAccount struct {
	Subject   string
	TeamID    SlackTeamID
	UserID    SlackUserID
	CreatedAt time.Time
}

func (a *GoogleWorkspaceAccount) Key() UserKey {
	return UserKey{TeamID: a.TeamID, UserID: a.UserID}
}

func (c *GoogleWorkspaceCredential) Key() UserKey {
	return UserKey{TeamID: c.TeamID, UserID: c.UserID}
}

// GmailSearchQuery searches the user's mailbox with Gmail's search syntax.
type GmailSearchQuery struct {
	Query      string
	MaxResults int
}

func (q GmailSearchQuery) Validate() error {
	if q.Query == "" {
		return goerr.New("empty gmail query")
	}
	if q.MaxResults < 1 || q.MaxResults > 20 {
		return goerr.New("gmail max results is out of range", goerr.V("max_results", q.MaxResults))
	}
	return nil
}

type GmailMessageSummary struct {
	ID       string
	ThreadID string
	From     string
	To       string
	Subject  string
	Date     string // the Date header as written
	Snippet  string
}

type GmailMessage struct {
	GmailMessageSummary
	// Body is the text/plain part, or the raw text/html part when there is
	// no text/plain part.
	Body string
}

// DriveSearchQuery searches the full text of the files the user can read.
type DriveSearchQuery struct {
	Query      string
	MaxResults int
}

func (q DriveSearchQuery) Validate() error {
	if q.Query == "" {
		return goerr.New("empty drive query")
	}
	if q.MaxResults < 1 || q.MaxResults > 20 {
		return goerr.New("drive max results is out of range", goerr.V("max_results", q.MaxResults))
	}
	return nil
}

type DriveFile struct {
	ID           string
	Name         string
	MimeType     string
	ModifiedTime time.Time
	WebViewLink  string
	Owners       []string // email addresses
}

type DriveFileText struct {
	DriveFile
	Text string
}

// calendarMaxRange is the widest period one listing reads.
const calendarMaxRange = 93 * 24 * time.Hour

// CalendarEventQuery lists the events of the user's primary calendar that
// overlap [TimeMin, TimeMax).
type CalendarEventQuery struct {
	TimeMin    time.Time
	TimeMax    time.Time
	Query      string
	MaxResults int
}

func (q CalendarEventQuery) Validate() error {
	if q.TimeMin.IsZero() || q.TimeMax.IsZero() {
		return goerr.New("calendar period needs both time_min and time_max")
	}
	if !q.TimeMin.Before(q.TimeMax) {
		return goerr.New("calendar time_min must be before time_max")
	}
	if q.TimeMax.Sub(q.TimeMin) > calendarMaxRange {
		return goerr.New("calendar period is longer than 93 days")
	}
	if q.MaxResults < 1 || q.MaxResults > 50 {
		return goerr.New("calendar max results is out of range", goerr.V("max_results", q.MaxResults))
	}
	return nil
}

type CalendarEvent struct {
	ID          string
	Summary     string
	Description string
	Location    string
	Start       string // RFC 3339, or a date for an all-day event
	End         string
	Organizer   string
	Attendees   []string
	HTMLLink    string
}

func (c *GoogleWorkspaceCredential) Validate() error {
	if err := c.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid google workspace credential key")
	}
	if err := c.RefreshToken.Validate(); err != nil {
		return goerr.Wrap(err, "invalid google workspace credential refresh token")
	}
	if len(c.Scopes) == 0 {
		return goerr.New("empty google workspace credential scopes")
	}
	if c.Subject == "" {
		return goerr.New("empty google workspace credential subject")
	}
	if c.Email == "" {
		return goerr.New("empty google workspace credential email")
	}
	if c.CreatedAt.IsZero() {
		return goerr.New("empty google workspace credential created_at")
	}
	if c.UpdatedAt.IsZero() {
		return goerr.New("empty google workspace credential updated_at")
	}
	return nil
}
