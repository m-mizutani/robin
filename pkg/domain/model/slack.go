package model

import (
	"regexp"

	"github.com/m-mizutani/goerr/v2"
)

// SlackTeamID is a Slack workspace ID such as "T0123ABCD".
type SlackTeamID string

// SlackUserID is a Slack user ID such as "U0123ABCD" or "W0123ABCD".
type SlackUserID string

// SlackUserToken is a plaintext Slack user token. It is never persisted and
// never logged; only its KMS ciphertext is stored.
type SlackUserToken string

var (
	slackTeamIDPattern = regexp.MustCompile(`^T[A-Z0-9]+$`)
	slackUserIDPattern = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
)

func (x SlackTeamID) Validate() error {
	if !slackTeamIDPattern.MatchString(string(x)) {
		return goerr.New("invalid slack team ID", goerr.V("team_id", string(x)))
	}
	return nil
}

func (x SlackUserID) Validate() error {
	if !slackUserIDPattern.MatchString(string(x)) {
		return goerr.New("invalid slack user ID", goerr.V("user_id", string(x)))
	}
	return nil
}

// UserKey identifies one user. Every piece of data that belongs to a user is
// stored under the document path derived from this key.
type UserKey struct {
	TeamID SlackTeamID
	UserID SlackUserID
}

func (k UserKey) Validate() error {
	if err := k.TeamID.Validate(); err != nil {
		return err
	}
	if err := k.UserID.Validate(); err != nil {
		return err
	}
	return nil
}

// SlackOAuthResult is what oauth.v2.access returns for the authorizing user.
type SlackOAuthResult struct {
	TeamID      SlackTeamID
	UserID      SlackUserID
	AccessToken SlackUserToken `masq:"secret"`
	TokenType   string
	Scopes      []string
}

// SlackIdentity is the team and user a token belongs to, as reported by
// auth.test.
type SlackIdentity struct {
	TeamID SlackTeamID
	UserID SlackUserID
}

// SlackThreadMessage is one message of a thread as Robin reads it.
type SlackThreadMessage struct {
	TS     string
	UserID SlackUserID // empty for a bot message
	BotID  string
	// FromRobin is set for an answer Robin posted.
	FromRobin bool
	Text      string
}

// SlackPostedMessage is a message Robin read back before deleting it.
type SlackPostedMessage struct {
	TS       string
	ThreadTS string // ts of the thread's parent; TS itself for a message outside a thread
	// Requester is the user whose mention made Robin post the message. It is
	// empty for a message Robin did not post.
	Requester SlackUserID
}

// SlackChannel is a channel as conversations.info returns it.
type SlackChannel struct {
	ID         string
	Name       string
	IsPrivate  bool
	IsArchived bool
}

// SlackMessageShortcut is a message shortcut a user chose on a message.
type SlackMessageShortcut struct {
	TeamID     SlackTeamID
	CallbackID string
	ChannelID  string
	UserID     SlackUserID // the user who chose the shortcut
	MessageTS  string
}

// Kinds of conversation a search result comes from. "group" and "im" are the
// values search.messages uses for a private channel and a DM.
const (
	SlackChannelTypePublic  = "channel"
	SlackChannelTypePrivate = "group"
	SlackChannelTypeIM      = "im"
	SlackChannelTypeMPIM    = "mpim"
)

// SlackMessageHit is one result of search.messages.
type SlackMessageHit struct {
	ChannelID   string
	ChannelName string
	// ChannelType is SlackChannelTypePublic, SlackChannelTypePrivate,
	// SlackChannelTypeIM or SlackChannelTypeMPIM.
	ChannelType string
	UserID      SlackUserID
	Username    string
	TS          string
	Text        string
	Permalink   string
}
