package interfaces

import (
	"context"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

// SlackOAuth exchanges an OAuth v2 authorization code.
type SlackOAuth interface {
	ExchangeCode(ctx context.Context, code, redirectURI string) (*model.SlackOAuthResult, error)
}

// SlackBot calls the Slack Web API with the bot token.
//
// requester is the user whose mention made Robin post a message. It is
// written into the block ID so that only that user can delete the message.
type SlackBot interface {
	PostEphemeral(ctx context.Context, channelID string, userID model.SlackUserID, threadTS, text string) error
	GetUserName(ctx context.Context, userID model.SlackUserID) (string, error)
	// PostProgress posts a message holding one context block and returns its ts.
	PostProgress(ctx context.Context, channelID, threadTS string, requester model.SlackUserID, text string) (string, error)
	// UpdateProgress fails with ErrSlackMessageNotFound when the message was deleted.
	UpdateProgress(ctx context.Context, channelID, messageTS string, requester model.SlackUserID, text string) error
	// PostAnswer posts markdown as markdown blocks, split into several messages
	// at line boundaries when it is longer than one message holds.
	PostAnswer(ctx context.Context, channelID, threadTS string, requester model.SlackUserID, markdown string) error
	// GetThreadMessages returns up to limit messages of the thread posted after
	// afterTS (empty: from the start) and before beforeTS, oldest first,
	// without Robin's progress messages.
	GetThreadMessages(ctx context.Context, channelID, threadTS, afterTS, beforeTS string, limit int) ([]model.SlackThreadMessage, error)
	// GetMessage reads one message of a thread or channel. It fails with
	// ErrSlackMessageNotFound when the message does not exist or Robin cannot read it.
	GetMessage(ctx context.Context, channelID, ts string) (*model.SlackPostedMessage, error)
	// DeleteMessage deletes a message Robin posted. It fails with
	// ErrSlackMessageNotFound when the message no longer exists.
	DeleteMessage(ctx context.Context, channelID, ts string) error
}

// SlackUserClientFactory builds a client authenticated with one user's token.
type SlackUserClientFactory interface {
	New(token model.SlackUserToken) SlackUserClient
}

// SlackUserClient calls the Slack Web API with one user's token. Methods that
// act on the user's behalf, such as message search, belong here.
type SlackUserClient interface {
	AuthTest(ctx context.Context) (*model.SlackIdentity, error)
	// SearchMessages runs search.messages: every message the user can see,
	// including private channels, DMs and group DMs. It fails with
	// ErrSlackTokenInvalid when Slack rejects the token.
	SearchMessages(ctx context.Context, query string, count int) ([]model.SlackMessageHit, error)
}
