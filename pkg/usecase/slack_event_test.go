package usecase_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"
	"github.com/slack-go/slack/slackevents"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
)

const (
	testChannel   = "C0123ABCD"
	testMessageTS = "1700000000.000100"
	testThreadTS  = "1699999999.000001"
)

type eventFixture struct {
	*authFixture
	llm    *fakeLLM
	agent  *usecase.Agent
	events *usecase.SlackEventUseCase
}

func newEventFixture(t *testing.T) *eventFixture {
	t.Helper()
	f := &eventFixture{authFixture: newAuthFixture(t), llm: &fakeLLM{}}
	agent, err := usecase.NewAgent(f.repo, f.llm, f.bot, usecase.AgentServices{}, testAgentConfig())
	gt.NoError(t, err).Required()
	agent.SetClockForTest(func() time.Time { return f.now }, func() string { return "id-" + f.now.String() })
	f.agent = agent
	f.events = usecase.NewSlackEventUseCase(f.repo, f.bot, f.access, agent, usecase.SlackEventConfig{
		TeamID:        testKey.TeamID,
		BaseURL:       testBaseURL,
		EventClaimTTL: 24 * time.Hour,
	})
	f.events.SetNowForTest(func() time.Time { return f.now })
	return f
}

func (f *eventFixture) connect(t *testing.T) {
	t.Helper()
	gt.NoError(t, f.access.Store(context.Background(), testKey, testUserToken, []string{"search:read"}, f.now)).Required()
}

func mentionEvent(eventID string, mention *slackevents.AppMentionEvent) *slackevents.EventsAPIEvent {
	return &slackevents.EventsAPIEvent{
		Type:   slackevents.CallbackEvent,
		TeamID: string(testKey.TeamID),
		Data:   &slackevents.EventsAPICallbackEvent{EventID: eventID, TeamID: string(testKey.TeamID)},
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: string(slackevents.AppMention),
			Data: mention,
		},
	}
}

func topLevelMention() *slackevents.AppMentionEvent {
	return &slackevents.AppMentionEvent{
		Type:      string(slackevents.AppMention),
		User:      string(testKey.UserID),
		Text:      "<@UBOT> hello",
		TimeStamp: testMessageTS,
		Channel:   testChannel,
	}
}

func isStartProgress(text string) bool {
	phrase, ok := strings.CutPrefix(text, ":thought_balloon: ")
	return ok && slices.Contains(usecase.StartPhrasesForTest, phrase)
}

func TestSlackEventUseCase_ConnectedUserTopLevel(t *testing.T) {
	f := newEventFixture(t)
	f.connect(t)
	f.llm.script(textTurn("Hello!", model.LLMUsage{}))

	gt.NoError(t, f.events.HandleEvent(context.Background(), mentionEvent("Ev001", topLevelMention()))).Required()

	gt.Number(t, f.factory.authTestCount()).Equal(1)
	gt.Equal(t, f.bot.methods(), []string{"PostProgress", "PostAnswer", "UpdateProgress"})
	progress := f.bot.recorded("PostProgress")[0]
	gt.String(t, progress.ChannelID).Equal(testChannel)
	gt.String(t, progress.ThreadTS).Equal(testMessageTS)
	gt.Value(t, progress.Requester).Equal(testKey.UserID)
	gt.True(t, isStartProgress(progress.Text))
	gt.Equal(t, f.bot.answers(), []botMessage{{ChannelID: testChannel, ThreadTS: testMessageTS, Text: "Hello!"}})
	update := f.bot.recorded("UpdateProgress")[0]
	gt.String(t, update.TS).Equal(progress.TS)
	gt.String(t, update.Text).Equal(":white_check_mark: Done · 1 LLM call · 0 tool calls · $0.00")
	gt.String(t, f.llm.recordedInputs()[0].UserText).Contains("<@UBOT> hello")
}

func TestSlackEventUseCase_ConnectedUserInThread(t *testing.T) {
	f := newEventFixture(t)
	f.connect(t)
	f.llm.script(textTurn("Hello!", model.LLMUsage{}))
	mention := topLevelMention()
	mention.ThreadTimeStamp = testThreadTS

	gt.NoError(t, f.events.HandleEvent(context.Background(), mentionEvent("Ev001", mention))).Required()

	answers := f.bot.answers()
	gt.Array(t, answers).Length(1).Required()
	gt.String(t, answers[0].ThreadTS).Equal(testThreadTS)
	gt.Equal(t, f.bot.texts("GetThreadMessages"), []string{"after= before=" + testMessageTS + " limit=50"})
}

func TestSlackEventUseCase_ThreadOfAnotherUser(t *testing.T) {
	f := newEventFixture(t)
	f.connect(t)
	other := model.UserKey{TeamID: testKey.TeamID, UserID: "U0999ZZZZ"}
	id, err := model.NewAgentSessionID(testKey.TeamID, testChannel, testThreadTS)
	gt.NoError(t, err).Required()
	_, err = f.repo.AgentSession().Begin(context.Background(), other, model.AgentSessionBeginRequest{
		ID: id, ChannelID: testChannel, ThreadTS: testThreadTS, LeaseID: "l", Now: f.now,
		LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "g",
	})
	gt.NoError(t, err).Required()
	mention := topLevelMention()
	mention.ThreadTimeStamp = testThreadTS

	gt.NoError(t, f.events.HandleEvent(context.Background(), mentionEvent("Ev001", mention))).Required()

	gt.Equal(t, f.bot.methods(), []string{"PostEphemeral"})
	gt.Equal(t, f.bot.ephemeralMessages(), []botMessage{{ChannelID: testChannel, UserID: testKey.UserID, ThreadTS: testThreadTS,
		Text: "In this thread, I only answer the person who started the conversation with me. Mention me in a new thread to start your own."}})
	gt.Number(t, f.factory.authTestCount()).Equal(0)
	gt.Array(t, f.llm.configs).Length(0)
}

func TestSlackEventUseCase_NotConnected(t *testing.T) {
	f := newEventFixture(t)
	mention := topLevelMention()
	mention.ThreadTimeStamp = testThreadTS

	gt.NoError(t, f.events.HandleEvent(context.Background(), mentionEvent("Ev001", mention))).Required()

	gt.Equal(t, f.bot.methods(), []string{"PostProgress", "UpdateProgress", "PostEphemeral"})
	gt.Equal(t, f.bot.texts("UpdateProgress"), []string{":lock: Sign in to Robin to use me"})
	ephemerals := f.bot.ephemeralMessages()
	gt.String(t, ephemerals[0].ChannelID).Equal(testChannel)
	gt.Value(t, ephemerals[0].UserID).Equal(testKey.UserID)
	gt.String(t, ephemerals[0].ThreadTS).Equal(testThreadTS)
	gt.String(t, ephemerals[0].Text).Contains(testBaseURL + "/login")
	gt.Number(t, f.cipher.decryptCount()).Equal(0)
	gt.Array(t, f.llm.configs).Length(0)
	f.assertNoSession(t, testThreadTS)
}

// assertNoSession checks that no conversation was started in the thread: the
// next Begin creates a new one.
func (f *eventFixture) assertNoSession(t *testing.T, threadTS string) {
	t.Helper()
	id, err := model.NewAgentSessionID(testKey.TeamID, testChannel, threadTS)
	gt.NoError(t, err).Required()
	res, err := f.repo.AgentSession().Begin(context.Background(), testKey, model.AgentSessionBeginRequest{
		ID: id, ChannelID: testChannel, ThreadTS: threadTS, LeaseID: "check", Now: f.now,
		LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "check",
	})
	gt.NoError(t, err).Required()
	gt.Value(t, res.Status).Equal(model.AgentSessionStarted)
}

func TestSlackEventUseCase_TokenInvalid(t *testing.T) {
	ctx := context.Background()
	f := newEventFixture(t)
	f.connect(t)
	f.factory.errs[testUserToken] = goerr.Wrap(interfaces.ErrSlackTokenInvalid, "token_revoked")

	gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).Required()

	_, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
	gt.Equal(t, f.bot.methods(), []string{"PostProgress", "UpdateProgress", "PostEphemeral"})
	gt.Equal(t, f.bot.texts("UpdateProgress"), []string{":lock: Sign in to Robin to use me"})
	gt.Array(t, f.llm.configs).Length(0)
	f.assertNoSession(t, testMessageTS)
}

func TestSlackEventUseCase_TokenOfAnotherUser(t *testing.T) {
	ctx := context.Background()
	f := newEventFixture(t)
	f.connect(t)
	f.factory.identities[testUserToken] = &model.SlackIdentity{TeamID: testKey.TeamID, UserID: "U9999ZZZZ"}

	gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).Required()

	_, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)
	gt.Equal(t, f.bot.methods(), []string{"PostProgress", "UpdateProgress", "PostEphemeral"})
	gt.Array(t, f.llm.configs).Length(0)
}

func TestSlackEventUseCase_AuthTestOtherError(t *testing.T) {
	ctx := context.Background()
	f := newEventFixture(t)
	f.connect(t)
	f.factory.errs[testUserToken] = errors.New("ratelimited")

	gt.Value(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).NotNil()

	_, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err)
	gt.Equal(t, f.bot.methods(), []string{"PostProgress", "UpdateProgress"})
	gt.Equal(t, f.bot.texts("UpdateProgress"), []string{":warning: Couldn't start this request. Please mention me again."})
	gt.Array(t, f.llm.configs).Length(0)
}

func TestSlackEventUseCase_DecryptError(t *testing.T) {
	ctx := context.Background()
	f := newEventFixture(t)
	f.connect(t)
	f.cipher.decryptErr = errors.New("permission denied")

	gt.Value(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).NotNil()

	_, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err)
	gt.Equal(t, f.bot.methods(), []string{"PostProgress", "UpdateProgress"})
	gt.Equal(t, f.bot.texts("UpdateProgress"), []string{":warning: Couldn't start this request. Please mention me again."})
}

func TestSlackEventUseCase_PostFailures(t *testing.T) {
	t.Run("progress message", func(t *testing.T) {
		f := newEventFixture(t)
		f.connect(t)
		f.bot.postErr = errors.New("channel_not_found")
		gt.Value(t, f.events.HandleEvent(context.Background(), mentionEvent("Ev001", topLevelMention()))).NotNil()
		gt.Number(t, f.factory.authTestCount()).Equal(0)
		gt.Array(t, f.llm.configs).Length(0)
	})

	t.Run("login prompt", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.ephemeralErr = errors.New("channel_not_found")
		gt.Value(t, f.events.HandleEvent(context.Background(), mentionEvent("Ev001", topLevelMention()))).NotNil()
	})
}

func TestSlackEventUseCase_DuplicateEvent(t *testing.T) {
	ctx := context.Background()
	f := newEventFixture(t)
	f.connect(t)
	f.llm.script(textTurn("Hello!", model.LLMUsage{}))

	gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).Required()
	gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).Required()

	gt.Array(t, f.bot.answers()).Length(1)
	gt.Array(t, f.bot.recorded("PostProgress")).Length(1)
	gt.Number(t, f.factory.authTestCount()).Equal(1)
}

func TestSlackEventUseCase_IgnoredEvents(t *testing.T) {
	cases := map[string]func() *slackevents.EventsAPIEvent{
		"bot mention": func() *slackevents.EventsAPIEvent {
			m := topLevelMention()
			m.BotID = "B0123"
			return mentionEvent("Ev001", m)
		},
		"another team": func() *slackevents.EventsAPIEvent {
			ev := mentionEvent("Ev001", topLevelMention())
			ev.TeamID = "T9999ZZZZ"
			return ev
		},
		"empty user": func() *slackevents.EventsAPIEvent {
			m := topLevelMention()
			m.User = ""
			return mentionEvent("Ev001", m)
		},
		"message event": func() *slackevents.EventsAPIEvent {
			return &slackevents.EventsAPIEvent{
				Type:   slackevents.CallbackEvent,
				TeamID: string(testKey.TeamID),
				Data:   &slackevents.EventsAPICallbackEvent{EventID: "Ev001"},
				InnerEvent: slackevents.EventsAPIInnerEvent{
					Type: string(slackevents.Message),
					Data: &slackevents.MessageEvent{User: string(testKey.UserID), Channel: testChannel},
				},
			}
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newEventFixture(t)
			f.connect(t)

			gt.NoError(t, f.events.HandleEvent(ctx, build())).Required()
			gt.Array(t, f.bot.recorded()).Length(0)
			gt.Number(t, f.factory.authTestCount()).Equal(0)

			// No claim was recorded, so the same event ID is still claimable.
			claimed, err := f.repo.SlackEvent().Claim(ctx, &model.SlackEventClaim{
				EventID: "Ev001", ClaimedAt: f.now, ExpiresAt: f.now.Add(time.Hour),
			})
			gt.NoError(t, err).Required()
			gt.Bool(t, claimed).True()
		})
	}
}

func TestStartPhrases(t *testing.T) {
	gt.Array(t, usecase.StartPhrasesForTest).Length(16)
	seen := map[string]bool{}
	for _, p := range usecase.StartPhrasesForTest {
		gt.False(t, seen[p])
		seen[p] = true
	}
}

func TestLifecycle_LoginThenMention(t *testing.T) {
	ctx := context.Background()
	f := newEventFixture(t)
	f.llm.script(textTurn("Hello!", model.LLMUsage{}))

	// 1. Not signed in: the mention gets only the login prompt.
	gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).Required()
	gt.Array(t, f.bot.ephemeralMessages()).Length(1).Required()
	gt.Bool(t, strings.Contains(f.bot.ephemeralMessages()[0].Text, testBaseURL+"/login")).True()
	gt.Array(t, f.bot.answers()).Length(0)
	_, err := f.repo.SlackCredential().Get(ctx, testKey)
	gt.Error(t, err).Is(interfaces.ErrNotFound)

	// 2. Sign in on the web.
	_, _, err = f.uc.HandleCallback(ctx, "auth-code")
	gt.NoError(t, err).Required()
	_, err = f.repo.SlackCredential().Get(ctx, testKey)
	gt.NoError(t, err).Required()

	// 3. The next mention gets an answer in the thread.
	gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev002", topLevelMention()))).Required()
	answers := f.bot.answers()
	gt.Array(t, answers).Length(1).Required()
	gt.String(t, answers[0].ThreadTS).Equal(testMessageTS)
	gt.String(t, answers[0].Text).Equal("Hello!")
	gt.Array(t, f.bot.ephemeralMessages()).Length(1)
}

func deleteShortcut(user model.SlackUserID) model.SlackMessageShortcut {
	return model.SlackMessageShortcut{
		TeamID:     testKey.TeamID,
		CallbackID: "robin_delete_message",
		ChannelID:  testChannel,
		UserID:     user,
		MessageTS:  "1700000300.000100",
	}
}

func robinMessage(requester model.SlackUserID) *model.SlackPostedMessage {
	return &model.SlackPostedMessage{TS: "1700000300.000100", ThreadTS: testThreadTS, Requester: requester}
}

func TestSlackEventUseCase_DeleteShortcut(t *testing.T) {
	ctx := context.Background()

	t.Run("by the requester", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.message = robinMessage(testKey.UserID)
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID))).Required()
		gt.Equal(t, f.bot.recorded(), []botCall{
			{Method: "GetMessage", ChannelID: testChannel, TS: "1700000300.000100"},
			{Method: "DeleteMessage", ChannelID: testChannel, TS: "1700000300.000100"},
			{Method: "PostEphemeral", ChannelID: testChannel, UserID: testKey.UserID, ThreadTS: testThreadTS, Text: "Deleted."},
		})
	})

	t.Run("by another user", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.message = robinMessage(testKey.UserID)
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, deleteShortcut("U0999ZZZZ"))).Required()
		gt.Equal(t, f.bot.methods(), []string{"GetMessage", "PostEphemeral"})
		gt.Equal(t, f.bot.ephemeralMessages(), []botMessage{{ChannelID: testChannel, UserID: "U0999ZZZZ", ThreadTS: testThreadTS,
			Text: "Only <@U0123ABCD>, who asked for this message, can delete it."}})
	})

	t.Run("not a message of Robin", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.message = robinMessage("")
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID))).Required()
		gt.Equal(t, f.bot.methods(), []string{"GetMessage", "PostEphemeral"})
		gt.Equal(t, f.bot.texts("PostEphemeral"), []string{"I can only delete messages I posted."})
	})

	t.Run("message not found", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.getErr = goerr.Wrap(interfaces.ErrSlackMessageNotFound, "thread_not_found")
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID))).Required()
		gt.Equal(t, f.bot.ephemeralMessages(), []botMessage{{ChannelID: testChannel, UserID: testKey.UserID, Text: "That message no longer exists."}})
	})

	t.Run("deleted in the meantime", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.message = robinMessage(testKey.UserID)
		f.bot.deleteErr = goerr.Wrap(interfaces.ErrSlackMessageNotFound, "message_not_found")
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID))).Required()
		gt.Equal(t, f.bot.texts("PostEphemeral"), []string{"That message no longer exists."})
	})

	t.Run("read failure", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.getErr = errors.New("ratelimited")
		gt.Error(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID)))
		gt.Equal(t, f.bot.methods(), []string{"GetMessage"})
	})

	t.Run("delete failure", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.message = robinMessage(testKey.UserID)
		f.bot.deleteErr = errors.New("cant_delete_message")
		gt.Error(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID)))
		gt.Equal(t, f.bot.texts("PostEphemeral"), []string{"I couldn't delete the message."})
	})

	t.Run("ignored shortcuts", func(t *testing.T) {
		f := newEventFixture(t)
		f.bot.message = robinMessage(testKey.UserID)
		other := deleteShortcut(testKey.UserID)
		other.TeamID = "T9999ZZZZ"
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, other)).Required()
		other = deleteShortcut(testKey.UserID)
		other.CallbackID = "something_else"
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, other)).Required()
		gt.Array(t, f.bot.recorded()).Length(0)
	})

	t.Run("conversation history is kept", func(t *testing.T) {
		f := newEventFixture(t)
		f.connect(t)
		f.llm.script(textTurn("Hello!", model.LLMUsage{}))
		gt.NoError(t, f.events.HandleEvent(ctx, mentionEvent("Ev001", topLevelMention()))).Required()
		id, err := model.NewAgentSessionID(testKey.TeamID, testChannel, testMessageTS)
		gt.NoError(t, err).Required()

		f.bot.message = robinMessage(testKey.UserID)
		gt.NoError(t, f.events.HandleMessageShortcut(ctx, deleteShortcut(testKey.UserID))).Required()

		res, err := f.repo.AgentSession().Begin(ctx, testKey, model.AgentSessionBeginRequest{
			ID: id, ChannelID: testChannel, ThreadTS: testMessageTS, LeaseID: "check", Now: f.now,
			LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "other",
		})
		gt.NoError(t, err).Required()
		gt.Value(t, res.Status).Equal(model.AgentSessionResumed)
		gt.Number(t, res.Session.MessageCount).Equal(2)
	})
}
