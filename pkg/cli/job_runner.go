package cli

import (
	"context"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/usecase/agents/hello"
)

const (
	// jobRunTimeout bounds one run of a job. A run still marked running after
	// it is shown as not finished.
	jobRunTimeout = 2 * time.Minute
	// jobRunTTL is how long a run record is kept.
	jobRunTTL = 30 * 24 * time.Hour
	// jobBatchSize is the number of due jobs read at a time.
	jobBatchSize = 100

	// A greeting is one or two sentences; the token limit leaves room for
	// the model's thinking.
	helloMaxTokens = 2048
	helloEffort    = anthropic.BetaOutputConfigEffortLow
)

// newJobRunners builds the runner of every job kind. Every trigger of the
// scheduler (the schedule command now) builds its runners here.
func newJobRunners(ctx context.Context, settings *config.Settings, llm *config.LLM, bot interfaces.SlackBot) (map[model.JobKind]usecase.JobRunner, error) {
	client, err := llm.NewClient(ctx, settings.Model, helloMaxTokens, helloEffort)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build the Claude client of the hello agent")
	}
	helloAgent, err := hello.New(client, bot, hello.Config{Rate: settings.Rate})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build the hello agent")
	}
	return map[model.JobKind]usecase.JobRunner{
		model.JobKindHello: helloAgent,
	}, nil
}

// schedulerConfig returns the scheduler settings shared by every trigger.
func schedulerConfig(s *config.Scheduler) usecase.SchedulerConfig {
	return usecase.SchedulerConfig{
		MaxDelay:    s.MaxDelay(),
		RunTimeout:  jobRunTimeout,
		RunTTL:      jobRunTTL,
		Concurrency: s.Concurrency(),
		BatchSize:   jobBatchSize,
	}
}
