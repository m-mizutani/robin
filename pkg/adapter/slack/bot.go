package slack

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"
	"github.com/slack-go/slack"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// Block ID prefixes of Robin's messages. The rest of the block ID is the user
// whose mention made Robin post the message; it decides who may delete it.
// Changing the format makes older messages undeletable, so keep reading the
// old one when it changes.
const (
	progressBlockPrefix = "robin_progress:"
	answerBlockPrefix   = "robin_answer:"
)

const (
	// answerChunkChars keeps one message below the 12,000 characters Slack
	// accepts in the markdown blocks of one message.
	answerChunkChars = 11000
	// notificationChars is the length of the fallback text that notifications
	// show.
	notificationChars = 3000
	repliesPageSize   = 200
	membersPageSize   = 200
)

// notFoundErrors are the Slack error codes that mean the message cannot be
// found or read by Robin.
var notFoundErrors = map[string]struct{}{
	"message_not_found": {},
	"thread_not_found":  {},
	"channel_not_found": {},
	"not_in_channel":    {},
}

// Bot calls the Slack Web API with the bot token.
type Bot struct {
	client *slack.Client
}

var _ interfaces.SlackBot = &Bot{}

func NewBot(botToken string, opts ...slack.Option) *Bot {
	return &Bot{client: slack.New(botToken, opts...)}
}

func wrapMessageError(err error, msg string, opts ...goerr.Option) error {
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		if _, ok := notFoundErrors[slackErr.Err]; ok {
			return goerr.Wrap(interfaces.ErrSlackMessageNotFound, msg, append(opts, goerr.V("slack_error", slackErr.Err))...)
		}
	}
	return wrapError(err, msg, opts...)
}

func (b *Bot) PostEphemeral(ctx context.Context, channelID string, userID model.SlackUserID, threadTS, text string) error {
	if _, err := b.client.PostEphemeralContext(ctx, channelID, string(userID),
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	); err != nil {
		return wrapError(err, "failed to post ephemeral message",
			goerr.V("channel_id", channelID), goerr.V("user_id", userID), goerr.V("thread_ts", threadTS))
	}
	return nil
}

func (b *Bot) GetUserName(ctx context.Context, userID model.SlackUserID) (string, error) {
	user, err := b.client.GetUserInfoContext(ctx, string(userID))
	if err != nil {
		return "", wrapError(err, "failed to get slack user info", goerr.V("user_id", userID))
	}
	if user.RealName != "" {
		return user.RealName, nil
	}
	if user.Name != "" {
		return user.Name, nil
	}
	return "", goerr.New("slack user has no name", goerr.V("user_id", userID))
}

func progressOptions(requester model.SlackUserID, text string) []slack.MsgOption {
	block := slack.NewContextBlock(progressBlockPrefix+string(requester),
		slack.NewTextBlockObject(slack.MarkdownType, text, false, false))
	return []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionBlocks(block),
	}
}

func (b *Bot) PostProgress(ctx context.Context, channelID, threadTS string, requester model.SlackUserID, text string) (string, error) {
	opts := append(progressOptions(requester, text), slack.MsgOptionTS(threadTS))
	_, ts, err := b.client.PostMessageContext(ctx, channelID, opts...)
	if err != nil {
		return "", wrapError(err, "failed to post progress message",
			goerr.V("channel_id", channelID), goerr.V("thread_ts", threadTS))
	}
	return ts, nil
}

func (b *Bot) UpdateProgress(ctx context.Context, channelID, messageTS string, requester model.SlackUserID, text string) error {
	if _, _, _, err := b.client.UpdateMessageContext(ctx, channelID, messageTS, progressOptions(requester, text)...); err != nil {
		return wrapMessageError(err, "failed to update progress message",
			goerr.V("channel_id", channelID), goerr.V("ts", messageTS))
	}
	return nil
}

func (b *Bot) PostAnswer(ctx context.Context, channelID, threadTS string, requester model.SlackUserID, markdown string) error {
	_, err := b.postAnswer(ctx, channelID, threadTS, requester, markdown)
	return err
}

func (b *Bot) PostMessage(ctx context.Context, channelID string, requester model.SlackUserID, markdown string) (string, error) {
	return b.postAnswer(ctx, channelID, "", requester, markdown)
}

// postAnswer posts markdown in parts and returns the ts of the first part. An
// empty threadTS posts to the channel itself.
func (b *Bot) postAnswer(ctx context.Context, channelID, threadTS string, requester model.SlackUserID, markdown string) (string, error) {
	first := ""
	for i, chunk := range splitAnswer(markdown, answerChunkChars) {
		opts := []slack.MsgOption{
			slack.MsgOptionText(truncateRunes(chunk, notificationChars), false),
			slack.MsgOptionBlocks(slack.NewMarkdownBlock(answerBlockPrefix+string(requester), chunk)),
		}
		if threadTS != "" {
			opts = append(opts, slack.MsgOptionTS(threadTS))
		}
		_, ts, err := b.client.PostMessageContext(ctx, channelID, opts...)
		if err != nil {
			return first, wrapError(err, "failed to post answer",
				goerr.V("channel_id", channelID), goerr.V("thread_ts", threadTS), goerr.V("part", i))
		}
		if first == "" {
			first = ts
		}
	}
	return first, nil
}

func (b *Bot) GetChannel(ctx context.Context, channelID string) (*model.SlackChannel, error) {
	ch, err := b.client.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID})
	if err != nil {
		var slackErr slack.SlackErrorResponse
		if errors.As(err, &slackErr) && slackErr.Err == "channel_not_found" {
			return nil, goerr.Wrap(interfaces.ErrSlackChannelNotFound, "slack channel not found", goerr.V("channel_id", channelID))
		}
		return nil, wrapError(err, "failed to read slack channel", goerr.V("channel_id", channelID))
	}
	return &model.SlackChannel{
		ID:         ch.ID,
		Name:       ch.Name,
		IsPrivate:  ch.IsPrivate,
		IsArchived: ch.IsArchived,
	}, nil
}

func (b *Bot) BotUserID(ctx context.Context) (model.SlackUserID, error) {
	res, err := b.client.AuthTestContext(ctx)
	if err != nil {
		return "", wrapError(err, "failed to identify the bot")
	}
	if res.UserID == "" {
		return "", goerr.New("auth.test returned no bot user ID")
	}
	return model.SlackUserID(res.UserID), nil
}

func (b *Bot) ChannelMembers(ctx context.Context, channelID string, users []model.SlackUserID) (map[model.SlackUserID]bool, error) {
	found := make(map[model.SlackUserID]bool, len(users))
	for _, u := range users {
		found[u] = false
	}
	remaining := len(found)
	cursor := ""
	for remaining > 0 {
		members, next, err := b.client.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
			ChannelID: channelID,
			Cursor:    cursor,
			Limit:     membersPageSize,
		})
		if err != nil {
			return nil, wrapError(err, "failed to read slack channel members", goerr.V("channel_id", channelID))
		}
		for _, m := range members {
			id := model.SlackUserID(m)
			if seen, ok := found[id]; ok && !seen {
				found[id] = true
				remaining--
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return found, nil
}

// splitAnswer splits text into parts of at most limit characters, at line
// boundaries where it can; a single longer line is cut by characters.
func splitAnswer(text string, limit int) []string {
	var parts []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
			curLen = 0
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		n := utf8.RuneCountInString(line)
		if curLen+n > limit {
			flush()
		}
		for n > limit {
			runes := []rune(line)
			parts = append(parts, string(runes[:limit]))
			line = string(runes[limit:])
			n -= limit
		}
		cur.WriteString(line)
		curLen += n
	}
	flush()
	if len(parts) == 0 {
		parts = []string{text}
	}
	return parts
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// robinBlock returns the requester written in a Robin block ID and which
// kind of message it is. ok is false for a message without one.
func robinBlock(msg slack.Message) (requester model.SlackUserID, answer bool, ok bool) {
	for _, block := range msg.Blocks.BlockSet {
		id := block.ID()
		switch {
		case strings.HasPrefix(id, progressBlockPrefix):
			return model.SlackUserID(strings.TrimPrefix(id, progressBlockPrefix)), false, true
		case strings.HasPrefix(id, answerBlockPrefix):
			return model.SlackUserID(strings.TrimPrefix(id, answerBlockPrefix)), true, true
		}
	}
	return "", false, false
}

func (b *Bot) GetThreadMessages(ctx context.Context, channelID, threadTS, afterTS, beforeTS string, limit int) ([]model.SlackThreadMessage, error) {
	var out []model.SlackThreadMessage
	cursor := ""
	for {
		msgs, hasMore, next, err := b.client.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTS,
			Cursor:    cursor,
			Oldest:    afterTS,
			Latest:    beforeTS,
			Limit:     repliesPageSize,
		})
		if err != nil {
			return nil, wrapError(err, "failed to read slack thread",
				goerr.V("channel_id", channelID), goerr.V("thread_ts", threadTS))
		}
		for _, m := range msgs {
			if (afterTS != "" && !tsAfter(m.Timestamp, afterTS)) || (beforeTS != "" && !tsAfter(beforeTS, m.Timestamp)) {
				continue
			}
			_, answer, robin := robinBlock(m)
			if robin && !answer {
				continue
			}
			out = append(out, model.SlackThreadMessage{
				TS:        m.Timestamp,
				UserID:    model.SlackUserID(m.User),
				BotID:     m.BotID,
				FromRobin: robin && answer,
				Text:      m.Text,
			})
		}
		if !hasMore || next == "" {
			break
		}
		cursor = next
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// tsAfter reports whether Slack timestamp a is later than b. Both have ten
// digits before the dot until the year 2286, so they compare as strings.
func tsAfter(a, b string) bool {
	return a > b
}

func (b *Bot) GetMessage(ctx context.Context, channelID, ts string) (*model.SlackPostedMessage, error) {
	// conversations.replies takes the ts of any message in a thread. The
	// documentation does not say whether the parent is always included, so
	// the message is picked by its ts.
	msgs, _, _, err := b.client.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: ts,
		Oldest:    ts,
		Latest:    ts,
		Inclusive: true,
		Limit:     2,
	})
	if err != nil {
		return nil, wrapMessageError(err, "failed to read slack message",
			goerr.V("channel_id", channelID), goerr.V("ts", ts))
	}
	for _, m := range msgs {
		if m.Timestamp != ts {
			continue
		}
		threadTS := m.ThreadTimestamp
		if threadTS == "" {
			threadTS = m.Timestamp
		}
		requester, _, _ := robinBlock(m)
		return &model.SlackPostedMessage{TS: m.Timestamp, ThreadTS: threadTS, Requester: requester}, nil
	}
	return nil, goerr.Wrap(interfaces.ErrSlackMessageNotFound, "slack message is not in the replies",
		goerr.V("channel_id", channelID), goerr.V("ts", ts))
}

func (b *Bot) DeleteMessage(ctx context.Context, channelID, ts string) error {
	if _, _, err := b.client.DeleteMessageContext(ctx, channelID, ts); err != nil {
		var slackErr slack.SlackErrorResponse
		if errors.As(err, &slackErr) && slackErr.Err == "message_not_found" {
			return goerr.Wrap(interfaces.ErrSlackMessageNotFound, "slack message is already deleted",
				goerr.V("channel_id", channelID), goerr.V("ts", ts))
		}
		return wrapError(err, "failed to delete slack message",
			goerr.V("channel_id", channelID), goerr.V("ts", ts))
	}
	return nil
}
