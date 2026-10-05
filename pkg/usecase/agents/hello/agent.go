// Package hello is the job that posts a short morning greeting written by
// the model to the user's job channel.
package hello

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/logging"
)

//go:embed prompt/system.md
var systemPrompt string

// ErrNoGreeting means the model returned no usable text (refusal, empty, or
// cut at the token limit).
var ErrNoGreeting = errors.New("model returned no greeting")

type Config struct {
	Rate model.Rate
	// MaxDelay is how late a greeting may still be posted. A morning greeting
	// posted at noon is of no use.
	MaxDelay time.Duration
}

func (c Config) Validate() error {
	if err := c.Rate.Validate(); err != nil {
		return goerr.Wrap(err, "invalid hello agent rate")
	}
	if c.MaxDelay <= 0 {
		return goerr.New("hello agent max delay must be positive", goerr.V("max_delay", c.MaxDelay))
	}
	return nil
}

// Agent writes one greeting with one model call and no tools, and posts it.
type Agent struct {
	llm interfaces.LLMClient
	bot interfaces.SlackBot
	cfg Config
}

var _ usecase.Job = &Agent{}

func New(llm interfaces.LLMClient, bot interfaces.SlackBot, cfg Config) (*Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Agent{llm: llm, bot: bot, cfg: cfg}, nil
}

func (a *Agent) MaxDelay() time.Duration { return a.cfg.MaxDelay }

// renderRequest gives the model the day of the run in the user's time zone.
func renderRequest(local time.Time, timeZone string) string {
	return fmt.Sprintf("Date: %s (%s)\nTime zone: %s\nWrite the greeting.",
		local.Format("2006-01-02"), local.Weekday(), timeZone)
}

func (a *Agent) Run(ctx context.Context, req usecase.JobRequest) error {
	vals := []goerr.Option{goerr.V("trigger_id", req.TriggerID), goerr.V("channel_id", req.ChannelID)}

	loc, err := model.LoadTimeZone(req.TimeZone)
	if err != nil {
		return goerr.Wrap(err, "failed to load the user's time zone", vals...)
	}

	session, err := a.llm.NewSession(model.LLMSessionConfig{SystemPrompt: systemPrompt}, nil)
	if err != nil {
		return goerr.Wrap(err, "failed to start the greeting conversation", vals...)
	}
	turn, err := session.Send(ctx, model.LLMInput{UserText: renderRequest(req.ScheduledAt.In(loc), req.TimeZone)})
	if err != nil {
		return goerr.Wrap(err, "failed to write the greeting", vals...)
	}
	logging.From(ctx).Info("hello agent model call",
		slog.String("trigger_id", string(req.TriggerID)),
		slog.String("stop_reason", string(turn.StopReason)),
		slog.Int64("input_tokens", turn.Usage.InputTokens),
		slog.Int64("output_tokens", turn.Usage.OutputTokens),
		slog.Int64("spent_nano_usd", int64(a.cfg.Rate.Cost(turn.Usage))))

	text := strings.TrimSpace(turn.Text())
	if turn.StopReason == model.LLMStopRefusal || turn.StopReason == model.LLMStopMaxTokens || text == "" {
		return goerr.Wrap(ErrNoGreeting, "no greeting to post", append(vals, goerr.V("stop_reason", turn.StopReason))...)
	}

	ts, err := a.bot.PostMessage(ctx, req.ChannelID, req.Key.UserID, text)
	if err != nil {
		return goerr.Wrap(err, "failed to post the greeting", vals...)
	}
	logging.From(ctx).Info("hello agent posted the greeting",
		slog.String("trigger_id", string(req.TriggerID)), slog.String("channel_id", req.ChannelID), slog.String("ts", ts))
	return nil
}
