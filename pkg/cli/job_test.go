package cli_test

import (
	"context"
	"testing"
	"time"

	"github.com/m-mizutani/gt"
	"github.com/urfave/cli/v3"

	robincli "github.com/m-mizutani/robin/pkg/cli"
	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase/usecasetest"
)

// Every job name this build defines has a job, so no trigger of a known job
// is skipped as undefined.
func TestNewJobs_CoversEveryJobName(t *testing.T) {
	clearServeEnv(t)
	var llm config.LLM
	cmd := &cli.Command{Name: "test", Flags: llm.Flags()}
	gt.NoError(t, cmd.Run(context.Background(), []string{"test", "--anthropic-api-key", "sk-ant-test"})).Required()

	var file config.File
	settings, err := file.Load(context.Background())
	gt.NoError(t, err).Required()

	jobs, err := robincli.NewJobsForTest(context.Background(), settings, &llm, usecasetest.NewSlackBot())
	gt.NoError(t, err).Required()
	gt.Number(t, len(jobs)).Equal(len(model.JobNames()))
	for _, name := range model.JobNames() {
		gt.Value(t, jobs[name]).NotNil()
	}
	gt.Value(t, jobs[model.JobNameHello].MaxDelay()).Equal(time.Hour)
}
