package slack

import (
	"context"
	"strings"

	"github.com/m-mizutani/goerr/v2"
	"github.com/slack-go/slack"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// UserClientFactory builds Slack clients authenticated with a user token.
type UserClientFactory struct {
	opts []slack.Option
}

var _ interfaces.SlackUserClientFactory = &UserClientFactory{}

func NewUserClientFactory(opts ...slack.Option) *UserClientFactory {
	return &UserClientFactory{opts: opts}
}

func (f *UserClientFactory) New(token model.SlackUserToken) interfaces.SlackUserClient {
	return &userClient{client: slack.New(string(token), f.opts...)}
}

type userClient struct {
	client *slack.Client
}

func (c *userClient) AuthTest(ctx context.Context) (*model.SlackIdentity, error) {
	resp, err := c.client.AuthTestContext(ctx)
	if err != nil {
		return nil, wrapError(err, "slack auth.test failed")
	}
	return &model.SlackIdentity{
		TeamID: model.SlackTeamID(resp.TeamID),
		UserID: model.SlackUserID(resp.UserID),
	}, nil
}

func (c *userClient) SearchMessages(ctx context.Context, query string, count int) ([]model.SlackMessageHit, error) {
	params := slack.NewSearchParameters()
	params.Count = count
	resp, err := c.client.SearchMessagesContext(ctx, query, params)
	if err != nil {
		return nil, wrapError(err, "slack search.messages failed", goerr.V("count", count))
	}
	hits := make([]model.SlackMessageHit, 0, len(resp.Matches))
	for _, m := range resp.Matches {
		hits = append(hits, model.SlackMessageHit{
			ChannelID:   m.Channel.ID,
			ChannelName: m.Channel.Name,
			ChannelType: searchChannelType(m),
			UserID:      model.SlackUserID(m.User),
			Username:    m.Username,
			TS:          m.Timestamp,
			Text:        m.Text,
			Permalink:   m.Permalink,
		})
	}
	return hits, nil
}

// searchChannelType classifies a match. The match type is "message" for a
// public channel, "group" for a private channel and "im" for a DM, and a
// group DM is marked only by channel.is_mpim; the flags are checked too
// because the documentation shows the types by example only.
func searchChannelType(m slack.SearchMessage) string {
	switch {
	case m.Type == model.SlackChannelTypeIM || strings.HasPrefix(m.Channel.ID, "D"):
		return model.SlackChannelTypeIM
	case m.Channel.IsMPIM:
		return model.SlackChannelTypeMPIM
	case m.Type == model.SlackChannelTypePrivate || m.Channel.IsPrivate:
		return model.SlackChannelTypePrivate
	default:
		return model.SlackChannelTypePublic
	}
}
