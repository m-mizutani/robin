package mention

import (
	"context"
	"encoding/json"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/usecase"
)

type slackSearchInput struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

type slackSearchHit struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ChannelType string `json:"channel_type"`
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	TS          string `json:"ts"`
	Text        string `json:"text"`
	Permalink   string `json:"permalink"`
}

func slackTools() []*agentTool {
	return []*agentTool{{
		service: "Slack",
		spec: modelToolSpec("slack_search_messages",
			"Search the Slack messages the requester can see, with Slack's search syntax (for example `in:#channel`, `from:@user`, `after:2026-01-01`). "+
				"Results include public channels (channel_type \"channel\"), private channels (\"group\"), DMs (\"im\") and group DMs (\"mpim\").",
			`{"type":"object","properties":{
				"query":{"type":"string","description":"Slack search query."},
				"count":{"type":"integer","minimum":1,"maximum":20,"description":"Number of results, 10 when omitted."}
			},"required":["query"]}`),
		describe: func(input json.RawMessage) string {
			in, _ := decodeInput[slackSearchInput](input)
			return "Searching Slack for " + quoted(in.Query)
		},
		run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
			in, err := decodeInput[slackSearchInput](input)
			if err != nil {
				return "", err
			}
			if err := requireString("query", in.Query); err != nil {
				return "", err
			}
			count, err := intInRange("count", in.Count, 10, 1, 20)
			if err != nil {
				return "", err
			}
			hits, err := req.Slack.SearchMessages(ctx, in.Query, count)
			if err != nil {
				return "", goerr.Wrap(err, "slack search failed")
			}
			out := make([]slackSearchHit, 0, len(hits))
			for _, h := range hits {
				out = append(out, slackSearchHit{
					ChannelID:   h.ChannelID,
					ChannelName: h.ChannelName,
					ChannelType: h.ChannelType,
					UserID:      string(h.UserID),
					Username:    h.Username,
					TS:          h.TS,
					Text:        h.Text,
					Permalink:   h.Permalink,
				})
			}
			return toJSON(out)
		},
	}}
}
