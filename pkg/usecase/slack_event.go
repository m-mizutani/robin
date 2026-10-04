package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/slack-go/slack/slackevents"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
	"github.com/m-mizutani/robin/pkg/utils/logging"
)

type SlackEventConfig struct {
	TeamID        model.SlackTeamID
	BaseURL       string
	EventClaimTTL time.Duration
}

type SlackEventUseCase struct {
	repo   interfaces.Repository
	bot    interfaces.SlackBot
	access *SlackUserAccess
	agent  *Agent
	cfg    SlackEventConfig
	now    func() time.Time
}

func NewSlackEventUseCase(repo interfaces.Repository, bot interfaces.SlackBot, access *SlackUserAccess,
	agent *Agent, cfg SlackEventConfig) *SlackEventUseCase {
	return &SlackEventUseCase{repo: repo, bot: bot, access: access, agent: agent, cfg: cfg, now: time.Now}
}

// deleteMessageCallbackID is the callback ID of the "Delete Robin message"
// message shortcut in the app manifest.
const deleteMessageCallbackID = "robin_delete_message"

// Replies to the "Delete Robin message" shortcut.
const (
	deletedText            = "Deleted."
	notRobinMessageText    = "I can only delete messages I posted."
	messageGoneText        = "That message no longer exists."
	deleteFailedText       = "I couldn't delete the message."
	notRequesterTextFormat = "Only <@%s>, who asked for this message, can delete it."
)

func (uc *SlackEventUseCase) loginPromptText() string {
	return fmt.Sprintf("To use this bot, sign in with your Slack account first: %s/login", uc.cfg.BaseURL)
}

// HandleEvent processes one Events API callback. Only app_mention is handled;
// every other event type is ignored.
func (uc *SlackEventUseCase) HandleEvent(ctx context.Context, event *slackevents.EventsAPIEvent) error {
	mention, ok := event.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if !ok {
		return nil
	}
	callback, ok := event.Data.(*slackevents.EventsAPICallbackEvent)
	if !ok {
		return goerr.New("app_mention event has no callback envelope")
	}
	return uc.handleAppMention(ctx, model.SlackTeamID(event.TeamID), callback.EventID, mention)
}

func (uc *SlackEventUseCase) handleAppMention(ctx context.Context, teamID model.SlackTeamID, eventID string, mention *slackevents.AppMentionEvent) error {
	if mention.BotID != "" || teamID != uc.cfg.TeamID || mention.User == "" {
		return nil
	}

	now := uc.now()
	claimed, err := uc.repo.SlackEvent().Claim(ctx, &model.SlackEventClaim{
		EventID:   eventID,
		ClaimedAt: now,
		ExpiresAt: now.Add(uc.cfg.EventClaimTTL),
	})
	if err != nil {
		return goerr.Wrap(err, "failed to claim slack event")
	}
	if !claimed {
		return nil
	}

	threadTS := mention.ThreadTimeStamp
	if threadTS == "" {
		threadTS = mention.TimeStamp
	}
	key := model.UserKey{TeamID: teamID, UserID: model.SlackUserID(mention.User)}
	vals := []goerr.Option{
		goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID),
		goerr.V("channel_id", mention.Channel), goerr.V("event_id", eventID),
	}

	ownedByOther, err := uc.agent.OwnedByOther(ctx, key, mention.Channel, threadTS)
	if err != nil {
		return goerr.Wrap(err, "failed to check the owner of the thread", vals...)
	}
	if ownedByOther {
		if err := uc.bot.PostEphemeral(ctx, mention.Channel, key.UserID, threadTS, notOwnerText); err != nil {
			return goerr.Wrap(err, "failed to tell the user that another user owns the thread", vals...)
		}
		return nil
	}

	progressTS, err := uc.bot.PostProgress(ctx, mention.Channel, threadTS, key.UserID, progressStart())
	if err != nil {
		return goerr.Wrap(err, "failed to post progress message", vals...)
	}
	// Until the agent takes over the progress message, every exit replaces
	// its first text, so the thread never keeps showing a run that is not
	// happening.
	setProgress := func(text string) {
		if err := uc.bot.UpdateProgress(ctx, mention.Channel, progressTS, key.UserID, text); err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "failed to update progress message", vals...), "progress update failed")
		}
	}
	signIn := func() { setProgress(progressSignIn) }

	client, err := uc.access.Client(ctx, key)
	if errors.Is(err, ErrSlackNotConnected) {
		signIn()
		return uc.promptLogin(ctx, mention.Channel, key.UserID, threadTS)
	}
	if err != nil {
		setProgress(progressStartFailed)
		return goerr.Wrap(err, "failed to get slack user client", vals...)
	}

	identity, err := client.AuthTest(ctx)
	switch {
	case errors.Is(err, interfaces.ErrSlackTokenInvalid):
		signIn()
		return uc.disconnectAndPrompt(ctx, client, key, mention.Channel, threadTS,
			goerr.Wrap(err, "slack user token is no longer usable", vals...))
	case err != nil:
		setProgress(progressStartFailed)
		return goerr.Wrap(err, "failed to verify slack user token", vals...)
	case identity.TeamID != key.TeamID || identity.UserID != key.UserID:
		signIn()
		return uc.disconnectAndPrompt(ctx, client, key, mention.Channel, threadTS,
			goerr.New("stored slack user token belongs to another user",
				append(vals, goerr.V("auth_test_team_id", identity.TeamID), goerr.V("auth_test_user_id", identity.UserID))...))
	}

	return uc.agent.Run(ctx, AgentRequest{
		Key:        key,
		Slack:      client,
		ChannelID:  mention.Channel,
		ThreadTS:   threadTS,
		MentionTS:  mention.TimeStamp,
		InThread:   mention.ThreadTimeStamp != "",
		Text:       mention.Text,
		ProgressTS: progressTS,
	})
}

// HandleMessageShortcut runs the "Delete Robin message" shortcut. Shortcuts
// of another workspace or with another callback ID are ignored. Only the user
// whose mention made Robin post the message may delete it; who that is comes
// from the message as Slack stores it, never from the request.
func (uc *SlackEventUseCase) HandleMessageShortcut(ctx context.Context, s model.SlackMessageShortcut) error {
	if s.TeamID != uc.cfg.TeamID || s.CallbackID != deleteMessageCallbackID || s.UserID == "" {
		return nil
	}
	vals := []goerr.Option{
		goerr.V("team_id", s.TeamID), goerr.V("user_id", s.UserID),
		goerr.V("channel_id", s.ChannelID), goerr.V("ts", s.MessageTS),
	}
	tell := func(threadTS, text string) error {
		if err := uc.bot.PostEphemeral(ctx, s.ChannelID, s.UserID, threadTS, text); err != nil {
			return goerr.Wrap(err, "failed to answer the delete shortcut", vals...)
		}
		return nil
	}

	msg, err := uc.bot.GetMessage(ctx, s.ChannelID, s.MessageTS)
	if errors.Is(err, interfaces.ErrSlackMessageNotFound) {
		return tell("", messageGoneText)
	}
	if err != nil {
		return goerr.Wrap(err, "failed to read the message to delete", vals...)
	}
	switch {
	case msg.Requester == "":
		return tell(msg.ThreadTS, notRobinMessageText)
	case msg.Requester != s.UserID:
		return tell(msg.ThreadTS, fmt.Sprintf(notRequesterTextFormat, msg.Requester))
	}

	err = uc.bot.DeleteMessage(ctx, s.ChannelID, msg.TS)
	switch {
	case errors.Is(err, interfaces.ErrSlackMessageNotFound):
		return tell(msg.ThreadTS, messageGoneText)
	case err != nil:
		if tellErr := tell(msg.ThreadTS, deleteFailedText); tellErr != nil {
			errutil.Handle(ctx, tellErr, "delete failure was not reported to the user")
		}
		return goerr.Wrap(err, "failed to delete robin message", vals...)
	}
	logging.From(ctx).Info("robin message deleted",
		slog.String("channel_id", s.ChannelID), slog.String("ts", msg.TS), slog.String("user_id", string(s.UserID)))
	return tell(msg.ThreadTS, deletedText)
}

func (uc *SlackEventUseCase) promptLogin(ctx context.Context, channelID string, userID model.SlackUserID, threadTS string) error {
	if err := uc.bot.PostEphemeral(ctx, channelID, userID, threadTS, uc.loginPromptText()); err != nil {
		return goerr.Wrap(err, "failed to post login prompt",
			goerr.V("channel_id", channelID), goerr.V("user_id", userID))
	}
	return nil
}

// disconnectAndPrompt deletes a token Slack no longer accepts, records why,
// and asks the user to sign in again.
func (uc *SlackEventUseCase) disconnectAndPrompt(ctx context.Context, client *UserClient, key model.UserKey, channelID, threadTS string, cause error) error {
	if err := uc.access.Disconnect(ctx, client); err != nil {
		return goerr.Wrap(err, "failed to disconnect unusable slack user token")
	}
	errutil.Handle(ctx, cause, "user token is no longer usable")
	return uc.promptLogin(ctx, channelID, key.UserID, threadTS)
}
