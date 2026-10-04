// Package hello is the job runner that posts a short morning greeting
// written by the model to the job's channel.
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
}

func (c Config) Validate() error {
	if err := c.Rate.Validate(); err != nil {
		return goerr.Wrap(err, "invalid hello agent rate")
	}
	return nil
}

// Agent writes one greeting with one model call and no tools, and posts it.
type Agent struct {
	llm interfaces.LLMClient
	bot interfaces.SlackBot
	cfg Config
}

var _ usecase.JobRunner = &Agent{}

func New(llm interfaces.LLMClient, bot interfaces.SlackBot, cfg Config) (*Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Agent{llm: llm, bot: bot, cfg: cfg}, nil
}

// renderRequest gives the model the day of the run in the job's time zone.
func renderRequest(local time.Time, timeZone, channelName string) string {
	return fmt.Sprintf("Date: %s (%s)\nTime zone: %s\nChannel: #%s\nWrite the greeting.",
		local.Format("2006-01-02"), local.Weekday(), timeZone, channelName)
}

func (a *Agent) Run(ctx context.Context, req usecase.JobRunRequest) (*usecase.JobRunResult, error) {
	vals := []goerr.Option{goerr.V("job_id", req.Job.ID), goerr.V("run_id", req.RunID), goerr.V("channel_id", req.Job.ChannelID)}

	loc, err := time.LoadLocation(req.Job.Schedule.TimeZone)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to load the job time zone", append(vals, goerr.V("time_zone", req.Job.Schedule.TimeZone))...)
	}

	session, err := a.llm.NewSession(model.LLMSessionConfig{SystemPrompt: systemPrompt}, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to start the greeting conversation", vals...)
	}
	turn, err := session.Send(ctx, model.LLMInput{
		UserText: renderRequest(req.ScheduledAt.In(loc), req.Job.Schedule.TimeZone, req.Job.ChannelName),
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to write the greeting", vals...)
	}

	result := &usecase.JobRunResult{Spent: a.cfg.Rate.Cost(turn.Usage)}
	logging.From(ctx).Info("hello agent model call",
		slog.String("job_id", string(req.Job.ID)),
		slog.String("stop_reason", string(turn.StopReason)),
		slog.Int64("input_tokens", turn.Usage.InputTokens),
		slog.Int64("output_tokens", turn.Usage.OutputTokens),
		slog.Int64("spent_nano_usd", int64(result.Spent)))

	text := strings.TrimSpace(turn.Text())
	if turn.StopReason == model.LLMStopRefusal || turn.StopReason == model.LLMStopMaxTokens || text == "" {
		return result, goerr.Wrap(ErrNoGreeting, "no greeting to post", append(vals, goerr.V("stop_reason", turn.StopReason))...)
	}

	ts, err := a.bot.PostMessage(ctx, req.Job.ChannelID, req.Key.UserID, text)
	if err != nil {
		return result, goerr.Wrap(err, "failed to post the greeting", vals...)
	}
	result.MessageTS = ts
	return result, nil
}
