package model

import (
	"regexp"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// AgentSessionID identifies the conversation of one Slack thread:
// "{TeamID}-{ChannelID}-{ThreadTS without the dot}". It is also a Firestore
// document ID.
type AgentSessionID string

var (
	agentSessionIDPattern = regexp.MustCompile(`^T[A-Z0-9]+-[CDG][A-Z0-9]+-[0-9]{16}$`)
	slackTSPattern        = regexp.MustCompile(`^[0-9]{10}\.[0-9]{6}$`)
)

func NewAgentSessionID(team SlackTeamID, channelID, threadTS string) (AgentSessionID, error) {
	if !slackTSPattern.MatchString(threadTS) {
		return "", goerr.New("invalid slack thread ts", goerr.V("thread_ts", threadTS))
	}
	id := AgentSessionID(string(team) + "-" + channelID + "-" + strings.ReplaceAll(threadTS, ".", ""))
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

func (x AgentSessionID) Validate() error {
	if !agentSessionIDPattern.MatchString(string(x)) {
		return goerr.New("invalid agent session ID", goerr.V("session_id", string(x)))
	}
	return nil
}

// agentSessionMessageMaxBytes keeps a message document under the Firestore
// limit of about 1 MiB.
const agentSessionMessageMaxBytes = 1_000_000

// AgentSession is the conversation between one user and Robin in one Slack
// thread. The lease marks a run in progress; Generation changes when an
// expired session is started again, so messages of the old generation are
// no longer read.
type AgentSession struct {
	TeamID         SlackTeamID
	UserID         SlackUserID
	ID             AgentSessionID
	ChannelID      string
	ThreadTS       string
	Generation     string
	MessageCount   int
	LastMentionTS  string // ts of the mention of the last committed run; empty before the first
	LeaseID        string // empty: no run holds the session
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
}

func (s *AgentSession) Key() UserKey {
	return UserKey{TeamID: s.TeamID, UserID: s.UserID}
}

func (s *AgentSession) Validate() error {
	if err := s.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid agent session key")
	}
	if err := s.ID.Validate(); err != nil {
		return err
	}
	if s.ChannelID == "" {
		return goerr.New("empty agent session channel ID")
	}
	if !slackTSPattern.MatchString(s.ThreadTS) {
		return goerr.New("invalid agent session thread ts", goerr.V("thread_ts", s.ThreadTS))
	}
	if s.Generation == "" {
		return goerr.New("empty agent session generation")
	}
	if s.MessageCount < 0 {
		return goerr.New("negative agent session message count", goerr.V("message_count", s.MessageCount))
	}
	if (s.LeaseID == "") != s.LeaseExpiresAt.IsZero() {
		return goerr.New("agent session has only part of the lease")
	}
	if s.CreatedAt.IsZero() {
		return goerr.New("empty agent session created_at")
	}
	if s.UpdatedAt.IsZero() {
		return goerr.New("empty agent session updated_at")
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		return goerr.New("agent session expires_at must be after created_at")
	}
	return nil
}

func (s *AgentSession) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// Leased reports whether a run holds an unexpired lease.
func (s *AgentSession) Leased(now time.Time) bool {
	return s.LeaseID != "" && now.Before(s.LeaseExpiresAt)
}

// AgentSessionMessage is one message of a conversation. Format and Data are
// written by the LLM adapter and never read by the usecase or the repository.
type AgentSessionMessage struct {
	Generation string
	Seq        int
	Format     string
	Data       []byte
	ExpiresAt  time.Time
}

func (m *AgentSessionMessage) Validate() error {
	if m.Generation == "" {
		return goerr.New("empty agent session message generation")
	}
	if m.Seq < 0 {
		return goerr.New("negative agent session message seq", goerr.V("seq", m.Seq))
	}
	if m.Format == "" {
		return goerr.New("empty agent session message format")
	}
	if len(m.Data) == 0 || len(m.Data) > agentSessionMessageMaxBytes {
		return goerr.New("agent session message data size is out of range", goerr.V("bytes", len(m.Data)))
	}
	if m.ExpiresAt.IsZero() {
		return goerr.New("empty agent session message expires_at")
	}
	return nil
}

// AgentThreadOwner records which user owns the conversation of a thread. It
// is written together with the session and never returned by the repository.
type AgentThreadOwner struct {
	TeamID    SlackTeamID
	UserID    SlackUserID
	SessionID AgentSessionID
	ExpiresAt time.Time
}

func (o *AgentThreadOwner) Key() UserKey {
	return UserKey{TeamID: o.TeamID, UserID: o.UserID}
}

func (o *AgentThreadOwner) Validate() error {
	if err := o.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid agent thread owner key")
	}
	if err := o.SessionID.Validate(); err != nil {
		return err
	}
	if o.ExpiresAt.IsZero() {
		return goerr.New("empty agent thread owner expires_at")
	}
	return nil
}

// AgentSessionBeginRequest starts or resumes a run. A new session gets
// ExpiresAt = Now + TTL and Generation = NewGeneration.
type AgentSessionBeginRequest struct {
	ID             AgentSessionID
	ChannelID      string
	ThreadTS       string
	LeaseID        string
	Now            time.Time
	LeaseExpiresAt time.Time
	TTL            time.Duration
	NewGeneration  string
}

func (r *AgentSessionBeginRequest) Validate() error {
	if err := r.ID.Validate(); err != nil {
		return err
	}
	if r.ChannelID == "" || !slackTSPattern.MatchString(r.ThreadTS) {
		return goerr.New("invalid agent session thread", goerr.V("channel_id", r.ChannelID), goerr.V("thread_ts", r.ThreadTS))
	}
	if r.LeaseID == "" || r.NewGeneration == "" {
		return goerr.New("empty agent session lease ID or generation")
	}
	if !r.LeaseExpiresAt.After(r.Now) {
		return goerr.New("agent session lease must expire after now")
	}
	if r.TTL <= 0 {
		return goerr.New("agent session TTL must be positive", goerr.V("ttl", r.TTL))
	}
	return nil
}

type AgentSessionBeginStatus string

const (
	AgentSessionStarted      AgentSessionBeginStatus = "started"
	AgentSessionResumed      AgentSessionBeginStatus = "resumed"
	AgentSessionBusy         AgentSessionBeginStatus = "busy"
	AgentSessionOwnedByOther AgentSessionBeginStatus = "owned_by_other"
)

// AgentSessionBeginResult carries the session, with the lease taken, only for
// AgentSessionStarted and AgentSessionResumed.
type AgentSessionBeginResult struct {
	Status  AgentSessionBeginStatus
	Session *AgentSession
}

// AgentSessionCommit appends Messages to the session. The repository sets
// Generation, Seq and ExpiresAt of each message.
type AgentSessionCommit struct {
	Messages      []*AgentSessionMessage
	LastMentionTS string
	Now           time.Time
}

func (c *AgentSessionCommit) Validate() error {
	if len(c.Messages) == 0 {
		return goerr.New("agent session commit has no messages")
	}
	if c.Now.IsZero() {
		return goerr.New("empty agent session commit time")
	}
	return nil
}
