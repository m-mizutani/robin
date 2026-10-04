package slack_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/gt"
	slackgo "github.com/slack-go/slack"

	"github.com/m-mizutani/robin/pkg/adapter/slack"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

func TestUserClient_AuthTest(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/auth.test": `{"ok":true,"team_id":"T0123ABCD","user_id":"U0123ABCD","user":"alice","team":"Example"}`,
	})
	client := slack.NewUserClientFactory(slackgo.OptionAPIURL(fake.apiURL())).New("xoxp-user-token")

	got, err := client.AuthTest(context.Background())
	gt.NoError(t, err).Required()
	gt.Value(t, got.TeamID).Equal(model.SlackTeamID("T0123ABCD"))
	gt.Value(t, got.UserID).Equal(model.SlackUserID("U0123ABCD"))

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Form.Get("token")).Equal("xoxp-user-token")
}

func TestUserClient_SearchMessages(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/search.messages": `{"ok":true,"query":"q","messages":{"total":4,"matches":[
			{"type":"message","channel":{"id":"C1","name":"general","is_private":false,"is_mpim":false},"user":"U1","username":"alice","ts":"1.1","text":"public","permalink":"https://example.slack.com/p1"},
			{"type":"group","channel":{"id":"C2","name":"secret","is_private":true,"is_mpim":false},"user":"U2","username":"bob","ts":"2.2","text":"private","permalink":"https://example.slack.com/p2"},
			{"type":"im","channel":{"id":"D3","name":"U9","is_private":true,"is_mpim":false},"user":"U3","username":"carol","ts":"3.3","text":"dm","permalink":"https://example.slack.com/p3"},
			{"type":"message","channel":{"id":"C4","name":"mpdm-a--b","is_private":true,"is_mpim":true},"user":"U4","username":"dave","ts":"4.4","text":"group dm","permalink":"https://example.slack.com/p4"}
		]}}`,
	})
	client := slack.NewUserClientFactory(slackgo.OptionAPIURL(fake.apiURL())).New("xoxp-user-token")

	got, err := client.SearchMessages(context.Background(), "budget plan", 5)
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.SlackMessageHit{
		{ChannelID: "C1", ChannelName: "general", ChannelType: "channel", UserID: "U1", Username: "alice", TS: "1.1", Text: "public", Permalink: "https://example.slack.com/p1"},
		{ChannelID: "C2", ChannelName: "secret", ChannelType: "group", UserID: "U2", Username: "bob", TS: "2.2", Text: "private", Permalink: "https://example.slack.com/p2"},
		{ChannelID: "D3", ChannelName: "U9", ChannelType: "im", UserID: "U3", Username: "carol", TS: "3.3", Text: "dm", Permalink: "https://example.slack.com/p3"},
		{ChannelID: "C4", ChannelName: "mpdm-a--b", ChannelType: "mpim", UserID: "U4", Username: "dave", TS: "4.4", Text: "group dm", Permalink: "https://example.slack.com/p4"},
	})

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Form.Get("query")).Equal("budget plan")
	gt.String(t, reqs[0].Form.Get("count")).Equal("5")
	gt.String(t, reqs[0].Form.Get("token")).Equal("xoxp-user-token")

	revoked := newFakeSlack(t, map[string]string{"/api/search.messages": `{"ok":false,"error":"invalid_auth"}`})
	_, err = slack.NewUserClientFactory(slackgo.OptionAPIURL(revoked.apiURL())).New("x").SearchMessages(context.Background(), "q", 5)
	gt.Error(t, err).Is(interfaces.ErrSlackTokenInvalid)
}

func TestUserClient_AuthTestErrors(t *testing.T) {
	cases := map[string]bool{
		"invalid_auth":     true,
		"not_authed":       true,
		"token_revoked":    true,
		"token_expired":    true,
		"account_inactive": true,
		"ratelimited":      false,
	}
	for code, invalid := range cases {
		t.Run(code, func(t *testing.T) {
			fake := newFakeSlack(t, map[string]string{
				"/api/auth.test": `{"ok":false,"error":"` + code + `"}`,
			})
			client := slack.NewUserClientFactory(slackgo.OptionAPIURL(fake.apiURL())).New("xoxp-user-token")

			_, err := client.AuthTest(context.Background())
			gt.Value(t, err).NotNil().Required()
			gt.Value(t, errors.Is(err, interfaces.ErrSlackTokenInvalid)).Equal(invalid)
		})
	}
}
