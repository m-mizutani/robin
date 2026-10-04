package memory

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type agentSessionKey struct {
	user model.UserKey
	id   model.AgentSessionID
}

type agentSessionRepository struct {
	mu       sync.Mutex
	sessions map[agentSessionKey]model.AgentSession
	messages map[agentSessionKey][]model.AgentSessionMessage
	// owners maps a thread to the user who owns its conversation.
	owners map[model.AgentSessionID]model.AgentThreadOwner
}

func newAgentSessionRepository() *agentSessionRepository {
	return &agentSessionRepository{
		sessions: make(map[agentSessionKey]model.AgentSession),
		messages: make(map[agentSessionKey][]model.AgentSessionMessage),
		owners:   make(map[model.AgentSessionID]model.AgentThreadOwner),
	}
}

func copyAgentSessionMessage(m model.AgentSessionMessage) model.AgentSessionMessage {
	m.Data = slices.Clone(m.Data)
	return m
}

func (r *agentSessionRepository) Begin(_ context.Context, key model.UserKey, req model.AgentSessionBeginRequest) (*model.AgentSessionBeginResult, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	if err := req.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid agent session begin request")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if owner, ok := r.owners[req.ID]; ok && owner.Key() != key && req.Now.Before(owner.ExpiresAt) {
		return &model.AgentSessionBeginResult{Status: model.AgentSessionOwnedByOther}, nil
	}

	k := agentSessionKey{user: key, id: req.ID}
	if session, ok := r.sessions[k]; ok && !session.Expired(req.Now) {
		if session.Leased(req.Now) {
			return &model.AgentSessionBeginResult{Status: model.AgentSessionBusy}, nil
		}
		session.LeaseID = req.LeaseID
		session.LeaseExpiresAt = req.LeaseExpiresAt
		if err := session.Validate(); err != nil {
			return nil, goerr.Wrap(err, "invalid agent session")
		}
		r.sessions[k] = session
		out := session
		return &model.AgentSessionBeginResult{Status: model.AgentSessionResumed, Session: &out}, nil
	}

	session := model.AgentSession{
		TeamID:         key.TeamID,
		UserID:         key.UserID,
		ID:             req.ID,
		ChannelID:      req.ChannelID,
		ThreadTS:       req.ThreadTS,
		Generation:     req.NewGeneration,
		LeaseID:        req.LeaseID,
		LeaseExpiresAt: req.LeaseExpiresAt,
		CreatedAt:      req.Now,
		UpdatedAt:      req.Now,
		ExpiresAt:      req.Now.Add(req.TTL),
	}
	owner := model.AgentThreadOwner{TeamID: key.TeamID, UserID: key.UserID, SessionID: req.ID, ExpiresAt: session.ExpiresAt}
	if err := session.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid agent session")
	}
	if err := owner.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid agent thread owner")
	}
	r.sessions[k] = session
	r.owners[req.ID] = owner
	out := session
	return &model.AgentSessionBeginResult{Status: model.AgentSessionStarted, Session: &out}, nil
}

func (r *agentSessionRepository) OwnedByOther(_ context.Context, key model.UserKey, id model.AgentSessionID, now time.Time) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid user key")
	}
	if err := id.Validate(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, ok := r.owners[id]
	return ok && owner.Key() != key && now.Before(owner.ExpiresAt), nil
}

func (r *agentSessionRepository) ListMessages(_ context.Context, key model.UserKey, id model.AgentSessionID, generation string) ([]*model.AgentSessionMessage, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*model.AgentSessionMessage
	for _, m := range r.messages[agentSessionKey{user: key, id: id}] {
		if m.Generation != generation {
			continue
		}
		c := copyAgentSessionMessage(m)
		out = append(out, &c)
	}
	slices.SortFunc(out, func(a, b *model.AgentSessionMessage) int { return a.Seq - b.Seq })
	return out, nil
}

func (r *agentSessionRepository) Commit(_ context.Context, key model.UserKey, id model.AgentSessionID, leaseID string, c model.AgentSessionCommit) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid user key")
	}
	if err := c.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid agent session commit")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	k := agentSessionKey{user: key, id: id}
	session, ok := r.sessions[k]
	if !ok || session.LeaseID != leaseID || !session.Leased(c.Now) {
		return false, nil
	}

	written := make([]model.AgentSessionMessage, 0, len(c.Messages))
	for i, m := range c.Messages {
		msg := copyAgentSessionMessage(*m)
		msg.Generation = session.Generation
		msg.Seq = session.MessageCount + i
		msg.ExpiresAt = session.ExpiresAt
		if err := msg.Validate(); err != nil {
			return false, goerr.Wrap(err, "invalid agent session message", goerr.V("seq", msg.Seq))
		}
		written = append(written, msg)
	}
	session.MessageCount += len(written)
	session.LastMentionTS = c.LastMentionTS
	session.UpdatedAt = c.Now
	session.LeaseID = ""
	session.LeaseExpiresAt = time.Time{}
	if err := session.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid agent session")
	}

	kept := slices.DeleteFunc(r.messages[k], func(m model.AgentSessionMessage) bool {
		return m.Generation != session.Generation
	})
	r.messages[k] = append(kept, written...)
	r.sessions[k] = session
	return true, nil
}

func (r *agentSessionRepository) Release(_ context.Context, key model.UserKey, id model.AgentSessionID, leaseID string) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := agentSessionKey{user: key, id: id}
	session, ok := r.sessions[k]
	if !ok || session.LeaseID != leaseID {
		return nil
	}
	session.LeaseID = ""
	session.LeaseExpiresAt = time.Time{}
	r.sessions[k] = session
	return nil
}

var _ interfaces.AgentSessionRepository = &agentSessionRepository{}
