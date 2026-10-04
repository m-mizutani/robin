package google_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/google"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

const tokenOK = `{"access_token":"ya29.access","token_type":"Bearer","expires_in":3600}`

func workspaceClient(f *fakeGoogle) interfaces.GoogleWorkspaceClient {
	return google.NewWorkspaceClientFactoryForTest("client-id", "client-secret", f.server.URL).New("refresh-token")
}

func apiRequests(f *fakeGoogle) []recordedRequest {
	var out []recordedRequest
	for _, r := range f.recorded() {
		if r.Path != "/token" {
			out = append(out, r)
		}
	}
	return out
}

func TestWorkspaceClient_SearchGmail(t *testing.T) {
	f := newFakeGoogle(t, map[string]fakeResponse{
		"/token":                         {status: http.StatusOK, body: tokenOK},
		"/gmail/v1/users/me/messages":    {status: http.StatusOK, body: `{"messages":[{"id":"m1","threadId":"t1"}]}`},
		"/gmail/v1/users/me/messages/m1": {status: http.StatusOK, body: `{"id":"m1","threadId":"t1","snippet":"hello","payload":{"headers":[{"name":"From","value":"a@example.com"},{"name":"To","value":"b@example.com"},{"name":"Subject","value":"Plan"},{"name":"Date","value":"Thu, 1 Oct 2026 10:00:00 +0900"}]}}`},
	})

	got, err := workspaceClient(f).SearchGmail(context.Background(), model.GmailSearchQuery{Query: "from:a@example.com", MaxResults: 5})
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.GmailMessageSummary{{ID: "m1", ThreadID: "t1", From: "a@example.com", To: "b@example.com", Subject: "Plan", Date: "Thu, 1 Oct 2026 10:00:00 +0900", Snippet: "hello"}})

	token := f.recorded()[0]
	gt.String(t, token.Path).Equal("/token")
	gt.String(t, token.Form.Get("grant_type")).Equal("refresh_token")
	gt.String(t, token.Form.Get("refresh_token")).Equal("refresh-token")
	gt.String(t, token.Form.Get("client_id")).Equal("client-id")

	reqs := apiRequests(f)
	gt.Array(t, reqs).Length(2).Required()
	gt.String(t, reqs[0].Query.Get("q")).Equal("from:a@example.com")
	gt.String(t, reqs[0].Query.Get("maxResults")).Equal("5")
	gt.String(t, reqs[0].Authorization).Equal("Bearer ya29.access")
	gt.String(t, reqs[1].Query.Get("format")).Equal("metadata")
	gt.Array(t, reqs[1].Query["metadataHeaders"]).Equal([]string{"From", "To", "Subject", "Date"})
}

func TestWorkspaceClient_GetGmailMessage(t *testing.T) {
	plain := base64.URLEncoding.EncodeToString([]byte("plain body"))
	html := base64.URLEncoding.EncodeToString([]byte("<p>html body</p>"))
	cases := map[string]struct {
		payload string
		want    string
	}{
		"text/plain preferred": {payload: `{"mimeType":"multipart/alternative","parts":[{"mimeType":"text/html","body":{"data":"` + html + `"}},{"mimeType":"text/plain","body":{"data":"` + plain + `"}}]}`, want: "plain body"},
		"html when no plain":   {payload: `{"mimeType":"multipart/alternative","parts":[{"mimeType":"text/html","body":{"data":"` + html + `"}}]}`, want: "<p>html body</p>"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t, map[string]fakeResponse{
				"/token":                         {status: http.StatusOK, body: tokenOK},
				"/gmail/v1/users/me/messages/m1": {status: http.StatusOK, body: `{"id":"m1","threadId":"t1","snippet":"s","payload":` + tc.payload + `}`},
			})
			got, err := workspaceClient(f).GetGmailMessage(context.Background(), "m1")
			gt.NoError(t, err).Required()
			gt.String(t, got.ID).Equal("m1")
			gt.String(t, got.Body).Equal(tc.want)
			gt.String(t, apiRequests(f)[0].Query.Get("format")).Equal("full")
		})
	}
}

func TestWorkspaceClient_SearchDrive(t *testing.T) {
	f := newFakeGoogle(t, map[string]fakeResponse{
		"/token":          {status: http.StatusOK, body: tokenOK},
		"/drive/v3/files": {status: http.StatusOK, body: `{"files":[{"id":"f1","name":"Plan","mimeType":"application/vnd.google-apps.document","modifiedTime":"2026-10-01T00:00:00Z","webViewLink":"https://docs.google.com/d/f1","owners":[{"emailAddress":"a@example.com"}]}]}`},
	})
	got, err := workspaceClient(f).SearchDrive(context.Background(), model.DriveSearchQuery{Query: `it's a "plan"`, MaxResults: 3})
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.DriveFile{{ID: "f1", Name: "Plan", MimeType: "application/vnd.google-apps.document", ModifiedTime: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), WebViewLink: "https://docs.google.com/d/f1", Owners: []string{"a@example.com"}}})

	q := apiRequests(f)[0].Query
	gt.String(t, q.Get("q")).Equal(`fullText contains 'it\'s a "plan"' and trashed = false`)
	gt.String(t, q.Get("pageSize")).Equal("3")
	gt.String(t, google.DriveQueryStringForTest(`a\b`)).Equal(`'a\\b'`)
}

func TestWorkspaceClient_GetDriveFileText(t *testing.T) {
	cases := map[string]struct {
		mimeType    string
		responses   map[string]fakeResponse
		want        string
		unsupported bool
	}{
		"document": {
			mimeType:  "application/vnd.google-apps.document",
			responses: map[string]fakeResponse{"/drive/v3/files/f1/export#media": {status: http.StatusOK, body: "doc text"}},
			want:      "doc text",
		},
		"spreadsheet": {
			mimeType:  "application/vnd.google-apps.spreadsheet",
			responses: map[string]fakeResponse{"/drive/v3/files/f1/export#media": {status: http.StatusOK, body: "a,b"}},
			want:      "a,b",
		},
		"text file": {
			mimeType:  "text/markdown",
			responses: map[string]fakeResponse{"/drive/v3/files/f1#media": {status: http.StatusOK, body: "# md"}},
			want:      "# md",
		},
		"pdf": {mimeType: "application/pdf", unsupported: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			responses := map[string]fakeResponse{
				"/token":             {status: http.StatusOK, body: tokenOK},
				"/drive/v3/files/f1": {status: http.StatusOK, body: `{"id":"f1","name":"F","mimeType":"` + tc.mimeType + `"}`},
			}
			for k, v := range tc.responses {
				responses[k] = v
			}
			f := newFakeGoogle(t, responses)
			got, err := workspaceClient(f).GetDriveFileText(context.Background(), "f1")
			if tc.unsupported {
				gt.Error(t, err).Is(interfaces.ErrGoogleUnsupportedFile)
				return
			}
			gt.NoError(t, err).Required()
			gt.String(t, got.Text).Equal(tc.want)
			gt.String(t, got.Name).Equal("F")
			reqs := apiRequests(f)
			gt.Array(t, reqs).Length(2).Required()
			if exportType := map[string]string{"document": "text/plain", "spreadsheet": "text/csv"}[name]; exportType != "" {
				gt.String(t, reqs[1].Query.Get("mimeType")).Equal(exportType)
			}
		})
	}
}

func TestWorkspaceClient_ListCalendarEvents(t *testing.T) {
	f := newFakeGoogle(t, map[string]fakeResponse{
		"/token": {status: http.StatusOK, body: tokenOK},
		"/calendar/v3/calendars/primary/events": {status: http.StatusOK, body: `{"items":[
			{"id":"e1","summary":"Review","location":"Room 1","start":{"dateTime":"2026-10-05T10:00:00+09:00"},"end":{"dateTime":"2026-10-05T11:00:00+09:00"},"organizer":{"email":"a@example.com"},"attendees":[{"email":"b@example.com"}],"htmlLink":"https://calendar.google.com/e1"},
			{"id":"e2","summary":"Holiday","start":{"date":"2026-10-06"},"end":{"date":"2026-10-07"}}
		]}`},
	})
	min := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	got, err := workspaceClient(f).ListCalendarEvents(context.Background(), model.CalendarEventQuery{TimeMin: min, TimeMax: min.Add(48 * time.Hour), Query: "Review", MaxResults: 25})
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.CalendarEvent{
		{ID: "e1", Summary: "Review", Location: "Room 1", Start: "2026-10-05T10:00:00+09:00", End: "2026-10-05T11:00:00+09:00", Organizer: "a@example.com", Attendees: []string{"b@example.com"}, HTMLLink: "https://calendar.google.com/e1"},
		{ID: "e2", Summary: "Holiday", Start: "2026-10-06", End: "2026-10-07"},
	})
	q := apiRequests(f)[0].Query
	gt.String(t, q.Get("timeMin")).Equal("2026-10-05T00:00:00Z")
	gt.String(t, q.Get("timeMax")).Equal("2026-10-07T00:00:00Z")
	gt.String(t, q.Get("q")).Equal("Review")
	gt.String(t, q.Get("singleEvents")).Equal("true")
	gt.String(t, q.Get("maxResults")).Equal("25")
}

func TestWorkspaceClient_Errors(t *testing.T) {
	t.Run("refresh token rejected", func(t *testing.T) {
		f := newFakeGoogle(t, map[string]fakeResponse{
			"/token": {status: http.StatusBadRequest, body: `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`},
		})
		_, err := workspaceClient(f).SearchGmail(context.Background(), model.GmailSearchQuery{Query: "x", MaxResults: 1})
		gt.Error(t, err).Is(interfaces.ErrGoogleTokenInvalid)
	})
	t.Run("not found", func(t *testing.T) {
		f := newFakeGoogle(t, map[string]fakeResponse{
			"/token":                         {status: http.StatusOK, body: tokenOK},
			"/gmail/v1/users/me/messages/m9": {status: http.StatusNotFound, body: `{"error":{"code":404,"message":"Requested entity was not found."}}`},
		})
		_, err := workspaceClient(f).GetGmailMessage(context.Background(), "m9")
		gt.Error(t, err).Is(interfaces.ErrGoogleNotFound)
	})
	t.Run("invalid query", func(t *testing.T) {
		f := newFakeGoogle(t, map[string]fakeResponse{})
		_, err := workspaceClient(f).ListCalendarEvents(context.Background(), model.CalendarEventQuery{MaxResults: 10})
		gt.Error(t, err)
		gt.Array(t, f.recorded()).Length(0)
	})
}
