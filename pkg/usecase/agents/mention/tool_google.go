package mention

import (
	"context"
	"encoding/json"
	"time"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
)

type googleSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type googleIDInput struct {
	MessageID string `json:"message_id"`
	FileID    string `json:"file_id"`
}

type calendarInput struct {
	TimeMin    string `json:"time_min"`
	TimeMax    string `json:"time_max"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func parseRFC3339(name, value string) (time.Time, error) {
	if err := requireString(name, value); err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, inputError("Invalid input: %s must be an RFC 3339 time such as 2026-10-04T09:00:00+09:00.", name)
	}
	return t, nil
}

func googleResult[T any](v T, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

func googleTools(google GoogleReader) []*agentTool {
	searchSchema := func(queryDescription string) string {
		return `{"type":"object","properties":{
			"query":{"type":"string","description":"` + queryDescription + `"},
			"max_results":{"type":"integer","minimum":1,"maximum":20,"description":"Number of results, 10 when omitted."}
		},"required":["query"]}`
	}
	decodeSearch := func(input json.RawMessage) (googleSearchInput, int, error) {
		in, err := decodeInput[googleSearchInput](input)
		if err != nil {
			return in, 0, err
		}
		if err := requireString("query", in.Query); err != nil {
			return in, 0, err
		}
		n, err := intInRange("max_results", in.MaxResults, 10, 1, 20)
		return in, n, err
	}

	return []*agentTool{
		{
			service: "Google Workspace",
			spec: modelToolSpec("gmail_search",
				"Search the requester's Gmail with Gmail's search syntax (for example `from:alice@example.com after:2026/09/01`). Returns sender, recipients, subject, date and a snippet.",
				searchSchema("Gmail search query.")),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[googleSearchInput](input)
				return "Searching Gmail for " + quoted(in.Query)
			},
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, n, err := decodeSearch(input)
				if err != nil {
					return "", err
				}
				return googleResult(google.SearchGmail(ctx, req.Key, model.GmailSearchQuery{Query: in.Query, MaxResults: n}))
			},
		},
		{
			service: "Google Workspace",
			spec: modelToolSpec("gmail_get_message", "Read the body of one email.",
				`{"type":"object","properties":{"message_id":{"type":"string","description":"ID from gmail_search."}},"required":["message_id"]}`),
			describe: func(json.RawMessage) string { return "Reading an email" },
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[googleIDInput](input)
				if err != nil {
					return "", err
				}
				if err := requireString("message_id", in.MessageID); err != nil {
					return "", err
				}
				return googleResult(google.GetGmailMessage(ctx, req.Key, in.MessageID))
			},
		},
		{
			service: "Google Workspace",
			spec: modelToolSpec("drive_search",
				"Search the full text of the Google Drive files the requester can read.",
				searchSchema("Words to search for.")),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[googleSearchInput](input)
				return "Searching Google Drive for " + quoted(in.Query)
			},
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, n, err := decodeSearch(input)
				if err != nil {
					return "", err
				}
				return googleResult(google.SearchDrive(ctx, req.Key, model.DriveSearchQuery{Query: in.Query, MaxResults: n}))
			},
		},
		{
			service: "Google Workspace",
			spec: modelToolSpec("drive_get_file",
				"Read a Google Docs, Sheets (as CSV) or Slides file, or a text file, as text. Other file types such as PDF cannot be read.",
				`{"type":"object","properties":{"file_id":{"type":"string","description":"ID from drive_search."}},"required":["file_id"]}`),
			describe: func(json.RawMessage) string { return "Reading a Google Drive file" },
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[googleIDInput](input)
				if err != nil {
					return "", err
				}
				if err := requireString("file_id", in.FileID); err != nil {
					return "", err
				}
				return googleResult(google.GetDriveFileText(ctx, req.Key, in.FileID))
			},
		},
		{
			service: "Google Workspace",
			spec: modelToolSpec("calendar_list_events",
				"List the events of the requester's primary Google Calendar between two times (at most 93 days apart).",
				`{"type":"object","properties":{
					"time_min":{"type":"string","description":"Start of the period, RFC 3339."},
					"time_max":{"type":"string","description":"End of the period, RFC 3339."},
					"query":{"type":"string","description":"Words in the events."},
					"max_results":{"type":"integer","minimum":1,"maximum":50,"description":"Number of events, 25 when omitted."}
				},"required":["time_min","time_max"]}`),
			describe: func(json.RawMessage) string { return "Checking Google Calendar" },
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[calendarInput](input)
				if err != nil {
					return "", err
				}
				minTime, err := parseRFC3339("time_min", in.TimeMin)
				if err != nil {
					return "", err
				}
				maxTime, err := parseRFC3339("time_max", in.TimeMax)
				if err != nil {
					return "", err
				}
				n, err := intInRange("max_results", in.MaxResults, 25, 1, 50)
				if err != nil {
					return "", err
				}
				q := model.CalendarEventQuery{TimeMin: minTime, TimeMax: maxTime, Query: in.Query, MaxResults: n}
				if err := q.Validate(); err != nil {
					return "", inputError("Invalid input: time_min must be before time_max, at most 93 days apart.")
				}
				return googleResult(google.ListCalendarEvents(ctx, req.Key, q))
			},
		},
	}
}
