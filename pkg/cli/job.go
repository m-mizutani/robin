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
	// jobRunTimeout bounds one run of a job.
	jobRunTimeout = 2 * time.Minute

	// A greeting is one or two sentences; the token limit leaves room for
	// the model's thinking.
	helloMaxTokens = 2048
	helloEffort    = anthropic.BetaOutputConfigEffortLow
	// helloMaxDelay skips a morning greeting that would be posted more than
	// an hour late.
	helloMaxDelay = time.Hour
)

// newJobs builds every job this build defines. Every trigger of the
// scheduler (the schedule command now) builds its jobs here.
func newJobs(ctx context.Context, settings *config.Settings, llm *config.LLM, bot interfaces.SlackBot) (map[model.JobName]usecase.Job, error) {
	client, err := llm.NewClient(ctx, settings.Model, helloMaxTokens, helloEffort)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build the Claude client of the hello agent")
	}
	helloAgent, err := hello.New(client, bot, hello.Config{Rate: settings.Rate, MaxDelay: helloMaxDelay})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build the hello agent")
	}
	return map[model.JobName]usecase.Job{
		model.JobNameHello: helloAgent,
	}, nil
}

// schedulerConfig returns the scheduler settings shared by every trigger.
func schedulerConfig(s *config.Scheduler) usecase.SchedulerConfig {
	return usecase.SchedulerConfig{
		RunTimeout:  jobRunTimeout,
		Concurrency: s.Concurrency(),
	}
}
