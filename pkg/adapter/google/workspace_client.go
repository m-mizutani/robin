package google

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/safe"
)

const (
	// maxGmailBodyChars bounds the body of one email.
	maxGmailBodyChars = 100_000
	// maxDriveFileBytes bounds what is read of one Drive file.
	maxDriveFileBytes = 1 << 20
)

// WorkspaceClientFactory builds clients that read Gmail, Drive and Calendar
// with one user's refresh token.
type WorkspaceClientFactory struct {
	clientID     string
	clientSecret string
	tokenURL     string
	httpClient   *http.Client
	// endpoints override the API base URLs in tests; empty uses Google's.
	gmailEndpoint    string
	driveEndpoint    string
	calendarEndpoint string
}

var _ interfaces.GoogleWorkspaceClientFactory = &WorkspaceClientFactory{}

// NewWorkspaceClientFactory takes the OAuth client the refresh tokens were
// issued to and the HTTP client that carries the request timeout.
func NewWorkspaceClientFactory(clientID, clientSecret string, httpClient *http.Client) *WorkspaceClientFactory {
	return &WorkspaceClientFactory{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     tokenURL,
		httpClient:   httpClient,
	}
}

func (f *WorkspaceClientFactory) New(token model.GoogleRefreshToken) interfaces.GoogleWorkspaceClient {
	return &workspaceClient{factory: f, token: token}
}

type workspaceClient struct {
	factory *WorkspaceClientFactory
	token   model.GoogleRefreshToken
}

// apiOptions builds an HTTP client that gets an access token from the
// refresh token and sends it with each request.
func (c *workspaceClient) apiOptions(ctx context.Context, endpoint string) []option.ClientOption {
	f := c.factory
	cfg := &oauth2.Config{
		ClientID:     f.clientID,
		ClientSecret: f.clientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: f.tokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
	client := cfg.Client(context.WithValue(ctx, oauth2.HTTPClient, f.httpClient), &oauth2.Token{RefreshToken: string(c.token)})
	client.Timeout = f.httpClient.Timeout
	opts := []option.ClientOption{option.WithHTTPClient(client)}
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}
	return opts
}

// wrapAPIError converts a refresh token rejected by Google's token endpoint
// into ErrGoogleTokenInvalid and a 404 into ErrGoogleNotFound.
func wrapAPIError(err error, msg string, opts ...goerr.Option) error {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
		return goerr.Wrap(interfaces.ErrGoogleTokenInvalid, msg, opts...)
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		opts = append(opts, goerr.V("status", apiErr.Code))
		switch apiErr.Code {
		case http.StatusNotFound:
			return goerr.Wrap(interfaces.ErrGoogleNotFound, msg, opts...)
		case http.StatusUnauthorized:
			return goerr.Wrap(interfaces.ErrGoogleTokenInvalid, msg, opts...)
		}
	}
	return goerr.Wrap(err, msg, opts...)
}

func (c *workspaceClient) gmail(ctx context.Context) (*gmail.Service, error) {
	srv, err := gmail.NewService(ctx, c.apiOptions(ctx, c.factory.gmailEndpoint)...)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build gmail client")
	}
	return srv, nil
}

func (c *workspaceClient) drive(ctx context.Context) (*drive.Service, error) {
	srv, err := drive.NewService(ctx, c.apiOptions(ctx, c.factory.driveEndpoint)...)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build drive client")
	}
	return srv, nil
}

func (c *workspaceClient) calendar(ctx context.Context) (*calendar.Service, error) {
	srv, err := calendar.NewService(ctx, c.apiOptions(ctx, c.factory.calendarEndpoint)...)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build calendar client")
	}
	return srv, nil
}

var gmailSummaryHeaders = []string{"From", "To", "Subject", "Date"}

func gmailSummary(m *gmail.Message) model.GmailMessageSummary {
	s := model.GmailMessageSummary{ID: m.Id, ThreadID: m.ThreadId, Snippet: m.Snippet}
	if m.Payload == nil {
		return s
	}
	for _, h := range m.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "from":
			s.From = h.Value
		case "to":
			s.To = h.Value
		case "subject":
			s.Subject = h.Value
		case "date":
			s.Date = h.Value
		}
	}
	return s
}

func (c *workspaceClient) SearchGmail(ctx context.Context, q model.GmailSearchQuery) ([]model.GmailMessageSummary, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	srv, err := c.gmail(ctx)
	if err != nil {
		return nil, err
	}
	list, err := srv.Users.Messages.List("me").Q(q.Query).MaxResults(int64(q.MaxResults)).Context(ctx).Do()
	if err != nil {
		return nil, wrapAPIError(err, "failed to search gmail")
	}
	out := make([]model.GmailMessageSummary, 0, len(list.Messages))
	for _, ref := range list.Messages {
		m, err := srv.Users.Messages.Get("me", ref.Id).Format("metadata").MetadataHeaders(gmailSummaryHeaders...).Context(ctx).Do()
		if err != nil {
			return nil, wrapAPIError(err, "failed to get gmail message metadata", goerr.V("message_id", ref.Id))
		}
		out = append(out, gmailSummary(m))
	}
	return out, nil
}

func decodeGmailBody(data string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(data)
		if err != nil {
			return "", goerr.Wrap(err, "failed to decode gmail body")
		}
	}
	return string(b), nil
}

// findPart returns the body data of the first part of mimeType.
func findPart(p *gmail.MessagePart, mimeType string) string {
	if p == nil {
		return ""
	}
	if p.MimeType == mimeType && p.Body != nil && p.Body.Data != "" {
		return p.Body.Data
	}
	for _, child := range p.Parts {
		if data := findPart(child, mimeType); data != "" {
			return data
		}
	}
	return ""
}

func truncateChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (c *workspaceClient) GetGmailMessage(ctx context.Context, id string) (*model.GmailMessage, error) {
	if id == "" {
		return nil, goerr.New("empty gmail message ID")
	}
	srv, err := c.gmail(ctx)
	if err != nil {
		return nil, err
	}
	m, err := srv.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, wrapAPIError(err, "failed to get gmail message", goerr.V("message_id", id))
	}
	data := findPart(m.Payload, "text/plain")
	if data == "" {
		data = findPart(m.Payload, "text/html")
	}
	body := ""
	if data != "" {
		if body, err = decodeGmailBody(data); err != nil {
			return nil, goerr.Wrap(err, "invalid gmail body", goerr.V("message_id", id))
		}
	}
	return &model.GmailMessage{GmailMessageSummary: gmailSummary(m), Body: truncateChars(body, maxGmailBodyChars)}, nil
}

const driveFileFields = "id,name,mimeType,modifiedTime,webViewLink,owners(emailAddress)"

func driveFile(f *drive.File) model.DriveFile {
	out := model.DriveFile{ID: f.Id, Name: f.Name, MimeType: f.MimeType, WebViewLink: f.WebViewLink}
	if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
		out.ModifiedTime = t
	}
	for _, o := range f.Owners {
		out.Owners = append(out.Owners, o.EmailAddress)
	}
	return out
}

// driveQueryString quotes a value for the Drive query language, which
// escapes a backslash and a single quote with a backslash.
func driveQueryString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (c *workspaceClient) SearchDrive(ctx context.Context, q model.DriveSearchQuery) ([]model.DriveFile, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	srv, err := c.drive(ctx)
	if err != nil {
		return nil, err
	}
	list, err := srv.Files.List().
		Q(fmt.Sprintf("fullText contains %s and trashed = false", driveQueryString(q.Query))).
		PageSize(int64(q.MaxResults)).
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		Corpora("allDrives").
		Fields(googleapi.Field("files(" + driveFileFields + ")")).
		Context(ctx).Do()
	if err != nil {
		return nil, wrapAPIError(err, "failed to search drive")
	}
	out := make([]model.DriveFile, 0, len(list.Files))
	for _, f := range list.Files {
		out = append(out, driveFile(f))
	}
	return out, nil
}

// driveExportTypes maps a Google editor file to the text format it is
// exported as.
var driveExportTypes = map[string]string{
	"application/vnd.google-apps.document":     "text/plain",
	"application/vnd.google-apps.spreadsheet":  "text/csv",
	"application/vnd.google-apps.presentation": "text/plain",
}

func isTextFile(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/") || mimeType == "application/json"
}

func (c *workspaceClient) GetDriveFileText(ctx context.Context, id string) (*model.DriveFileText, error) {
	if id == "" {
		return nil, goerr.New("empty drive file ID")
	}
	srv, err := c.drive(ctx)
	if err != nil {
		return nil, err
	}
	f, err := srv.Files.Get(id).SupportsAllDrives(true).Fields(googleapi.Field(driveFileFields)).Context(ctx).Do()
	if err != nil {
		return nil, wrapAPIError(err, "failed to get drive file", goerr.V("file_id", id))
	}

	var resp *http.Response
	switch exportType, ok := driveExportTypes[f.MimeType]; {
	case ok:
		resp, err = srv.Files.Export(id, exportType).Context(ctx).Download()
	case isTextFile(f.MimeType):
		resp, err = srv.Files.Get(id).SupportsAllDrives(true).Context(ctx).Download()
	default:
		return nil, goerr.Wrap(interfaces.ErrGoogleUnsupportedFile, "drive file cannot be read as text",
			goerr.V("file_id", id), goerr.V("mime_type", f.MimeType))
	}
	if err != nil {
		return nil, wrapAPIError(err, "failed to download drive file", goerr.V("file_id", id))
	}
	defer safe.Close(ctx, resp.Body)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDriveFileBytes))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read drive file", goerr.V("file_id", id))
	}
	return &model.DriveFileText{DriveFile: driveFile(f), Text: strings.ToValidUTF8(string(body), "")}, nil
}

func eventTime(t *calendar.EventDateTime) string {
	if t == nil {
		return ""
	}
	if t.DateTime != "" {
		return t.DateTime
	}
	return t.Date
}

func (c *workspaceClient) ListCalendarEvents(ctx context.Context, q model.CalendarEventQuery) ([]model.CalendarEvent, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	srv, err := c.calendar(ctx)
	if err != nil {
		return nil, err
	}
	call := srv.Events.List("primary").
		TimeMin(q.TimeMin.Format(time.RFC3339)).
		TimeMax(q.TimeMax.Format(time.RFC3339)).
		MaxResults(int64(q.MaxResults)).
		SingleEvents(true).
		OrderBy("startTime")
	if q.Query != "" {
		call = call.Q(q.Query)
	}
	events, err := call.Context(ctx).Do()
	if err != nil {
		return nil, wrapAPIError(err, "failed to list calendar events")
	}
	out := make([]model.CalendarEvent, 0, len(events.Items))
	for _, e := range events.Items {
		ev := model.CalendarEvent{
			ID:          e.Id,
			Summary:     e.Summary,
			Description: e.Description,
			Location:    e.Location,
			Start:       eventTime(e.Start),
			End:         eventTime(e.End),
			HTMLLink:    e.HtmlLink,
		}
		if e.Organizer != nil {
			ev.Organizer = e.Organizer.Email
		}
		for _, a := range e.Attendees {
			ev.Attendees = append(ev.Attendees, a.Email)
		}
		out = append(out, ev)
	}
	return out, nil
}
