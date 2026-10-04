package mention

import (
	"context"
	"encoding/json"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
)

// The readers are the parts of the usecase Access components the agent
// uses. They keep the agent away from tokens and let its tests replace the
// integrations.

// NotionReader reads Notion with one user's connection.
type NotionReader interface {
	Connection(ctx context.Context, key model.UserKey) (*usecase.NotionStatus, error)
	Search(ctx context.Context, key model.UserKey, q model.NotionSearchQuery) (*model.NotionList, error)
	GetPage(ctx context.Context, key model.UserKey, id model.NotionObjectID) (json.RawMessage, error)
	ListBlockChildren(ctx context.Context, key model.UserKey, id model.NotionObjectID, page model.NotionPagination) (*model.NotionList, error)
	GetDatabase(ctx context.Context, key model.UserKey, id model.NotionObjectID) (json.RawMessage, error)
	GetDataSource(ctx context.Context, key model.UserKey, id model.NotionObjectID) (json.RawMessage, error)
	QueryDataSource(ctx context.Context, key model.UserKey, id model.NotionObjectID, q model.NotionDataSourceQuery) (*model.NotionList, error)
}

// GoogleReader reads Gmail, Drive and Calendar with one user's connection.
type GoogleReader interface {
	Connection(ctx context.Context, key model.UserKey) (*usecase.GoogleWorkspaceStatus, error)
	SearchGmail(ctx context.Context, key model.UserKey, q model.GmailSearchQuery) ([]model.GmailMessageSummary, error)
	GetGmailMessage(ctx context.Context, key model.UserKey, id string) (*model.GmailMessage, error)
	SearchDrive(ctx context.Context, key model.UserKey, q model.DriveSearchQuery) ([]model.DriveFile, error)
	GetDriveFileText(ctx context.Context, key model.UserKey, id string) (*model.DriveFileText, error)
	ListCalendarEvents(ctx context.Context, key model.UserKey, q model.CalendarEventQuery) ([]model.CalendarEvent, error)
}

// GitHubReader gives a GitHub client of one user's connection.
type GitHubReader interface {
	Connection(ctx context.Context, key model.UserKey) (*usecase.GitHubStatus, error)
	Client(ctx context.Context, key model.UserKey) (interfaces.GitHubUserClient, error)
}

var (
	_ NotionReader = (*usecase.NotionAccess)(nil)
	_ GoogleReader = (*usecase.GoogleWorkspaceAccess)(nil)
	_ GitHubReader = (*usecase.GitHubUserAccess)(nil)
)

// Services holds the integrations enabled on this server; a nil reader
// means the integration is disabled.
type Services struct {
	Notion NotionReader
	Google GoogleReader
	GitHub GitHubReader
}
