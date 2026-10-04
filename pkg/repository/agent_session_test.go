package repository_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type agentThread struct {
	id        model.AgentSessionID
	channelID string
	threadTS  string
}

// randomAgentThread keeps parallel runs against one Firestore from sharing
// threads.
func randomAgentThread(t *testing.T, team model.SlackTeamID) agentThread {
	t.Helper()
	channelID := "C" + randomSuffix(t)
	threadTS := fmt.Sprintf("%010d.%06d", rand.IntN(1_000_000_000)+1_000_000_000, rand.IntN(1_000_000))
	id, err := model.NewAgentSessionID(team, channelID, threadTS)
	gt.NoError(t, err).Required()
	return agentThread{id: id, channelID: channelID, threadTS: threadTS}
}

func beginRequest(th agentThread, leaseID string, now time.Time) model.AgentSessionBeginRequest {
	return model.AgentSessionBeginRequest{
		ID:             th.id,
		ChannelID:      th.channelID,
		ThreadTS:       th.threadTS,
		LeaseID:        leaseID,
		Now:            now,
		LeaseExpiresAt: now.Add(11 * time.Minute),
		TTL:            720 * time.Hour,
		NewGeneration:  "gen-" + leaseID,
	}
}

func agentMessages(texts ...string) []*model.AgentSessionMessage {
	out := make([]*model.AgentSessionMessage, 0, len(texts))
	for _, s := range texts {
		out = append(out, &model.AgentSessionMessage{Format: "test.v1", Data: []byte(s)})
	}
	return out
}

func TestAgentSessionRepository(t *testing.T) {
	runRepositoryTest(t, "begin starts a new session", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()

		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionStarted)
		s := res.Session
		gt.Value(t, s.TeamID).Equal(key.TeamID)
		gt.Value(t, s.UserID).Equal(key.UserID)
		gt.Value(t, s.ID).Equal(th.id)
		gt.String(t, s.ChannelID).Equal(th.channelID)
		gt.String(t, s.ThreadTS).Equal(th.threadTS)
		gt.String(t, s.Generation).Equal("gen-lease-1")
		gt.Number(t, s.MessageCount).Equal(0)
		gt.String(t, s.LastMentionTS).Equal("")
		gt.String(t, s.LeaseID).Equal("lease-1")
		timeEqual(t, s.LeaseExpiresAt, now.Add(11*time.Minute))
		timeEqual(t, s.CreatedAt, now)
		timeEqual(t, s.UpdatedAt, now)
		timeEqual(t, s.ExpiresAt, now.Add(720*time.Hour))
	})

	runRepositoryTest(t, "begin by another user while the owner's session is alive", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		owner := randomUserKey(t)
		other := model.UserKey{TeamID: owner.TeamID, UserID: model.SlackUserID("U" + randomSuffix(t))}
		th := randomAgentThread(t, owner.TeamID)
		now := time.Now().UTC()

		_, err := repo.AgentSession().Begin(ctx, owner, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()

		res, err := repo.AgentSession().Begin(ctx, other, beginRequest(th, "lease-2", now))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionOwnedByOther)
		gt.Value(t, res.Session).Nil()

		byOther, err := repo.AgentSession().OwnedByOther(ctx, other, th.id, now)
		gt.NoError(t, err).Required()
		gt.True(t, byOther)
		byOwner, err := repo.AgentSession().OwnedByOther(ctx, owner, th.id, now)
		gt.NoError(t, err).Required()
		gt.False(t, byOwner)

		// The other user got nothing written: after the owner's session
		// expires, the other user starts a fresh session.
		later := now.Add(721 * time.Hour)
		res, err = repo.AgentSession().Begin(ctx, other, beginRequest(th, "lease-3", later))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionStarted)
		byOwner, err = repo.AgentSession().OwnedByOther(ctx, owner, th.id, later)
		gt.NoError(t, err).Required()
		gt.True(t, byOwner)
	})

	runRepositoryTest(t, "owned by other is false for an unknown thread", func(t *testing.T, repo interfaces.Repository) {
		key := randomUserKey(t)
		got, err := repo.AgentSession().OwnedByOther(testContext(t), key, randomAgentThread(t, key.TeamID).id, time.Now())
		gt.NoError(t, err).Required()
		gt.False(t, got)
	})

	runRepositoryTest(t, "begin while leased is busy", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()
		_, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()

		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", now.Add(time.Minute)))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionBusy)

		// The lease is unchanged: lease-1 can still commit.
		ok, err := repo.AgentSession().Commit(ctx, key, th.id, "lease-1", model.AgentSessionCommit{Messages: agentMessages("a"), LastMentionTS: th.threadTS, Now: now.Add(2 * time.Minute)})
		gt.NoError(t, err).Required()
		gt.True(t, ok)
	})

	runRepositoryTest(t, "begin resumes after release and after the lease expires", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()
		_, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()
		ok, err := repo.AgentSession().Commit(ctx, key, th.id, "lease-1", model.AgentSessionCommit{Messages: agentMessages("a", "b"), LastMentionTS: "1.0", Now: now})
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", now.Add(time.Minute)))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionResumed)
		gt.String(t, res.Session.Generation).Equal("gen-lease-1")
		gt.Number(t, res.Session.MessageCount).Equal(2)
		gt.String(t, res.Session.LeaseID).Equal("lease-2")
		gt.String(t, res.Session.LastMentionTS).Equal("1.0")

		// lease-2 is never released; once it expires the next run resumes.
		res, err = repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-3", now.Add(13*time.Minute)))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionResumed)
		gt.String(t, res.Session.LeaseID).Equal("lease-3")

		gt.NoError(t, repo.AgentSession().Release(ctx, key, th.id, "lease-3")).Required()
		res, err = repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-4", now.Add(14*time.Minute)))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionResumed)
	})

	runRepositoryTest(t, "begin after expiry starts a new generation", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()
		_, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()
		ok, err := repo.AgentSession().Commit(ctx, key, th.id, "lease-1", model.AgentSessionCommit{Messages: agentMessages("old"), LastMentionTS: "1.0", Now: now})
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		later := now.Add(720 * time.Hour)
		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", later))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionStarted)
		gt.String(t, res.Session.Generation).Equal("gen-lease-2")
		gt.Number(t, res.Session.MessageCount).Equal(0)
		gt.String(t, res.Session.LastMentionTS).Equal("")

		ok, err = repo.AgentSession().Commit(ctx, key, th.id, "lease-2", model.AgentSessionCommit{Messages: agentMessages("new"), LastMentionTS: "2.0", Now: later})
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		msgs, err := repo.AgentSession().ListMessages(ctx, key, th.id, "gen-lease-2")
		gt.NoError(t, err).Required()
		gt.Array(t, msgs).Length(1).Required()
		gt.String(t, string(msgs[0].Data)).Equal("new")
	})

	runRepositoryTest(t, "commit appends messages in order", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()
		_, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()

		committedAt := now.Add(time.Minute)
		ok, err := repo.AgentSession().Commit(ctx, key, th.id, "lease-1", model.AgentSessionCommit{Messages: agentMessages("m0", "m1", "m2"), LastMentionTS: "1700000000.000200", Now: committedAt})
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		msgs, err := repo.AgentSession().ListMessages(ctx, key, th.id, "gen-lease-1")
		gt.NoError(t, err).Required()
		gt.Array(t, msgs).Length(3).Required()
		for i, m := range msgs {
			gt.String(t, m.Generation).Equal("gen-lease-1")
			gt.Number(t, m.Seq).Equal(i)
			gt.String(t, m.Format).Equal("test.v1")
			gt.String(t, string(m.Data)).Equal(fmt.Sprintf("m%d", i))
			timeEqual(t, m.ExpiresAt, now.Add(720*time.Hour))
		}

		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", committedAt))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionResumed)
		gt.Number(t, res.Session.MessageCount).Equal(3)
		gt.String(t, res.Session.LastMentionTS).Equal("1700000000.000200")
		timeEqual(t, res.Session.UpdatedAt, committedAt)

		ok, err = repo.AgentSession().Commit(ctx, key, th.id, "lease-2", model.AgentSessionCommit{Messages: agentMessages("m3", "m4"), LastMentionTS: "1700000000.000300", Now: committedAt})
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		msgs, err = repo.AgentSession().ListMessages(ctx, key, th.id, "gen-lease-1")
		gt.NoError(t, err).Required()
		gt.Array(t, msgs).Length(5).Required()
		gt.Number(t, msgs[3].Seq).Equal(3)
		gt.String(t, string(msgs[4].Data)).Equal("m4")
	})

	runRepositoryTest(t, "commit without the lease writes nothing", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()
		_, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()

		ok, err := repo.AgentSession().Commit(ctx, key, th.id, "other", model.AgentSessionCommit{Messages: agentMessages("x"), LastMentionTS: "1.0", Now: now})
		gt.NoError(t, err).Required()
		gt.False(t, ok)
		msgs, err := repo.AgentSession().ListMessages(ctx, key, th.id, "gen-lease-1")
		gt.NoError(t, err).Required()
		gt.Array(t, msgs).Length(0)

		// The lease is still held by lease-1.
		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", now))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionBusy)
	})

	runRepositoryTest(t, "release", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()

		gt.NoError(t, repo.AgentSession().Release(ctx, key, th.id, "none"))

		_, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()
		gt.NoError(t, repo.AgentSession().Release(ctx, key, th.id, "other")).Required()
		res, err := repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", now))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionBusy)

		gt.NoError(t, repo.AgentSession().Release(ctx, key, th.id, "lease-1")).Required()
		res, err = repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-2", now))
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionResumed)
	})

	runRepositoryTest(t, "invalid input is rejected", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		th := randomAgentThread(t, key.TeamID)
		now := time.Now().UTC()

		bad := beginRequest(th, "lease-1", now)
		bad.ID = "not-an-id"
		_, err := repo.AgentSession().Begin(ctx, key, bad)
		gt.Error(t, err)

		_, err = repo.AgentSession().Begin(ctx, key, beginRequest(th, "lease-1", now))
		gt.NoError(t, err).Required()
		large := &model.AgentSessionMessage{Format: "test.v1", Data: []byte(strings.Repeat("a", 1_000_001))}
		_, err = repo.AgentSession().Commit(ctx, key, th.id, "lease-1", model.AgentSessionCommit{Messages: []*model.AgentSessionMessage{large}, Now: now})
		gt.Error(t, err)
		msgs, err := repo.AgentSession().ListMessages(ctx, key, th.id, "gen-lease-1")
		gt.NoError(t, err).Required()
		gt.Array(t, msgs).Length(0)
	})
}
