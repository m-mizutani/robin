package cli

import (
	"context"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
)

// Run executes the command line. An error that stops the command is recorded
// here, so main only has to exit with a non-zero status.
func Run(ctx context.Context, args []string, version string) error {
	var logger config.Logger

	app := &cli.Command{
		Name:    "robin",
		Usage:   "AI agent that works as a Slack bot and a web UI",
		Version: version,
		Flags:   logger.Flags(),
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			return ctx, logger.Configure()
		},
		Commands: []*cli.Command{
			cmdServe(),
			cmdSchedule(),
		},
	}

	if err := app.Run(ctx, args); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to run app"), "failed to run app")
		return err
	}
	return nil
}
