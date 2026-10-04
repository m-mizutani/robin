package firestore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type agentSessionRepository struct {
	client *firestore.Client
}

func (r *agentSessionRepository) doc(key model.UserKey, id model.AgentSessionID) *firestore.DocumentRef {
	return userDoc(r.client, key).Collection(agentSessionsCollection).Doc(string(id))
}

func (r *agentSessionRepository) messages(key model.UserKey, id model.AgentSessionID) *firestore.CollectionRef {
	return r.doc(key, id).Collection(agentSessionMessagesCollection)
}

func messageDocID(generation string, seq int) string {
	return fmt.Sprintf("%s-%08d", generation, seq)
}

// ownerDoc holds the owner of one thread. It lives outside the user's
// document because it has to be found by the thread alone.
func (r *agentSessionRepository) ownerDoc(id model.AgentSessionID) *firestore.DocumentRef {
	return r.client.Collection(agentThreadsCollection).Doc(string(id))
}

func (r *agentSessionRepository) session(tx *firestore.Transaction, key model.UserKey, id model.AgentSessionID) (*model.AgentSession, error) {
	snap, err := tx.Get(r.doc(key, id))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, goerr.Wrap(err, "failed to get agent session")
	}
	var s model.AgentSession
	if err := snap.DataTo(&s); err != nil {
		return nil, goerr.Wrap(err, "failed to decode agent session")
	}
	if s.Key() != key {
		return nil, goerr.Wrap(interfaces.ErrKeyMismatch, "stored agent session belongs to another key")
	}
	return &s, nil
}

func decodeOwner(snap *firestore.DocumentSnapshot, err error) (*model.AgentThreadOwner, error) {
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, goerr.Wrap(err, "failed to get agent thread owner")
	}
	var owner model.AgentThreadOwner
	if err := snap.DataTo(&owner); err != nil {
		return nil, goerr.Wrap(err, "failed to decode agent thread owner")
	}
	return &owner, nil
}

func (r *agentSessionRepository) Begin(ctx context.Context, key model.UserKey, req model.AgentSessionBeginRequest) (*model.AgentSessionBeginResult, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	if err := req.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid agent session begin request")
	}

	var result *model.AgentSessionBeginResult
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		result = nil
		owner, err := decodeOwner(tx.Get(r.ownerDoc(req.ID)))
		if err != nil {
			return err
		}
		session, err := r.session(tx, key, req.ID)
		if err != nil {
			return err
		}

		if owner != nil && owner.Key() != key && req.Now.Before(owner.ExpiresAt) {
			result = &model.AgentSessionBeginResult{Status: model.AgentSessionOwnedByOther}
			return nil
		}

		if session != nil && !session.Expired(req.Now) {
			if session.Leased(req.Now) {
				result = &model.AgentSessionBeginResult{Status: model.AgentSessionBusy}
				return nil
			}
			session.LeaseID = req.LeaseID
			session.LeaseExpiresAt = req.LeaseExpiresAt
			if err := session.Validate(); err != nil {
				return goerr.Wrap(err, "invalid agent session")
			}
			if err := tx.Set(r.doc(key, req.ID), session); err != nil {
				return goerr.Wrap(err, "failed to set agent session lease")
			}
			result = &model.AgentSessionBeginResult{Status: model.AgentSessionResumed, Session: session}
			return nil
		}

		next := &model.AgentSession{
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
		nextOwner := &model.AgentThreadOwner{TeamID: key.TeamID, UserID: key.UserID, SessionID: req.ID, ExpiresAt: next.ExpiresAt}
		if err := next.Validate(); err != nil {
			return goerr.Wrap(err, "invalid agent session")
		}
		if err := nextOwner.Validate(); err != nil {
			return goerr.Wrap(err, "invalid agent thread owner")
		}
		if err := tx.Set(r.doc(key, req.ID), next); err != nil {
			return goerr.Wrap(err, "failed to put agent session")
		}
		if err := tx.Set(r.ownerDoc(req.ID), nextOwner); err != nil {
			return goerr.Wrap(err, "failed to put agent thread owner")
		}
		result = &model.AgentSessionBeginResult{Status: model.AgentSessionStarted, Session: next}
		return nil
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to begin agent session",
			goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID), goerr.V("session_id", req.ID))
	}
	return result, nil
}

func (r *agentSessionRepository) OwnedByOther(ctx context.Context, key model.UserKey, id model.AgentSessionID, now time.Time) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid user key")
	}
	if err := id.Validate(); err != nil {
		return false, err
	}
	owner, err := decodeOwner(r.ownerDoc(id).Get(ctx))
	if err != nil {
		return false, goerr.Wrap(err, "failed to read agent thread owner", goerr.V("session_id", id))
	}
	return owner != nil && owner.Key() != key && now.Before(owner.ExpiresAt), nil
}

func (r *agentSessionRepository) ListMessages(ctx context.Context, key model.UserKey, id model.AgentSessionID, generation string) ([]*model.AgentSessionMessage, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}

	// One equality filter uses the automatic single-field index; ordering by
	// Seq too would need a composite index, so the order is set here.
	iter := r.messages(key, id).Where("Generation", "==", generation).Documents(ctx)
	defer iter.Stop()
	var out []*model.AgentSessionMessage
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list agent session messages", goerr.V("session_id", id))
		}
		var m model.AgentSessionMessage
		if err := snap.DataTo(&m); err != nil {
			return nil, goerr.Wrap(err, "failed to decode agent session message", goerr.V("doc_id", snap.Ref.ID))
		}
		out = append(out, &m)
	}
	slices.SortFunc(out, func(a, b *model.AgentSessionMessage) int { return a.Seq - b.Seq })
	return out, nil
}

func (r *agentSessionRepository) Commit(ctx context.Context, key model.UserKey, id model.AgentSessionID, leaseID string, c model.AgentSessionCommit) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid user key")
	}
	if err := c.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid agent session commit")
	}

	var committed bool
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		committed = false
		session, err := r.session(tx, key, id)
		if err != nil {
			return err
		}
		if session == nil || session.LeaseID != leaseID || !session.Leased(c.Now) {
			return nil
		}

		for i, m := range c.Messages {
			msg := *m
			msg.Generation = session.Generation
			msg.Seq = session.MessageCount + i
			msg.ExpiresAt = session.ExpiresAt
			if err := msg.Validate(); err != nil {
				return goerr.Wrap(err, "invalid agent session message", goerr.V("seq", msg.Seq))
			}
			if err := tx.Set(r.messages(key, id).Doc(messageDocID(msg.Generation, msg.Seq)), &msg); err != nil {
				return goerr.Wrap(err, "failed to put agent session message", goerr.V("seq", msg.Seq))
			}
		}
		session.MessageCount += len(c.Messages)
		session.LastMentionTS = c.LastMentionTS
		session.UpdatedAt = c.Now
		session.LeaseID = ""
		session.LeaseExpiresAt = time.Time{}
		if err := session.Validate(); err != nil {
			return goerr.Wrap(err, "invalid agent session")
		}
		if err := tx.Set(r.doc(key, id), session); err != nil {
			return goerr.Wrap(err, "failed to update agent session")
		}
		committed = true
		return nil
	})
	if err != nil {
		return false, goerr.Wrap(err, "failed to commit agent session",
			goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID), goerr.V("session_id", id))
	}
	return committed, nil
}

func (r *agentSessionRepository) Release(ctx context.Context, key model.UserKey, id model.AgentSessionID, leaseID string) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		session, err := r.session(tx, key, id)
		if err != nil {
			return err
		}
		if session == nil || session.LeaseID != leaseID {
			return nil
		}
		session.LeaseID = ""
		session.LeaseExpiresAt = time.Time{}
		if err := tx.Set(r.doc(key, id), session); err != nil {
			return goerr.Wrap(err, "failed to release agent session lease")
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to release agent session",
			goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID), goerr.V("session_id", id))
	}
	return nil
}
