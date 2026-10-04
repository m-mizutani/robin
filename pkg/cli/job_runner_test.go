package cli_test

import (
	"context"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/urfave/cli/v3"

	robincli "github.com/m-mizutani/robin/pkg/cli"
	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase/usecasetest"
)

// Every kind this build defines has a runner, so no job of a known kind is
// recorded as having none.
func TestNewJobRunners_CoversEveryKind(t *testing.T) {
	clearServeEnv(t)
	var llm config.LLM
	cmd := &cli.Command{Name: "test", Flags: llm.Flags()}
	gt.NoError(t, cmd.Run(context.Background(), []string{"test", "--anthropic-api-key", "sk-ant-test"})).Required()

	var file config.File
	settings, err := file.Load(context.Background())
	gt.NoError(t, err).Required()

	runners, err := robincli.NewJobRunnersForTest(context.Background(), settings, &llm, usecasetest.NewSlackBot())
	gt.NoError(t, err).Required()
	gt.Number(t, len(runners)).Equal(len(model.JobKinds()))
	for _, kind := range model.JobKinds() {
		gt.Value(t, runners[kind]).NotNil()
	}
}
