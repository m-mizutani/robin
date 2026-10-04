package interfaces

import (
	"context"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

// GoogleOAuth talks to Google's OAuth 2.0 and OpenID Connect endpoints.
type GoogleOAuth interface {
	// AuthorizeURL returns the authorization URL. It always asks for offline
	// access with the consent prompt, so Google returns a refresh token.
	AuthorizeURL(state, redirectURI string, scopes []string) string
	ExchangeCode(ctx context.Context, code, redirectURI string) (*model.GoogleOAuthResult, error)
	FetchIdentity(ctx context.Context, accessToken model.GoogleAccessToken) (*model.GoogleIdentity, error)
	// Revoke revokes an access or refresh token. A token Google rejects as
	// invalid is reported as ErrGoogleTokenInvalid.
	Revoke(ctx context.Context, token string) error
}

// GoogleWorkspaceClientFactory builds a client that reads Google Workspace
// with one user's refresh token.
type GoogleWorkspaceClientFactory interface {
	New(token model.GoogleRefreshToken) GoogleWorkspaceClient
}

// GoogleWorkspaceClient fails with ErrGoogleTokenInvalid when Google rejects
// the refresh token and with ErrGoogleNotFound on 404.
type GoogleWorkspaceClient interface {
	SearchGmail(ctx context.Context, q model.GmailSearchQuery) ([]model.GmailMessageSummary, error)
	GetGmailMessage(ctx context.Context, id string) (*model.GmailMessage, error)
	SearchDrive(ctx context.Context, q model.DriveSearchQuery) ([]model.DriveFile, error)
	// GetDriveFileText fails with ErrGoogleUnsupportedFile for a file it cannot turn into text.
	GetDriveFileText(ctx context.Context, id string) (*model.DriveFileText, error)
	ListCalendarEvents(ctx context.Context, q model.CalendarEventQuery) ([]model.CalendarEvent, error)
}
