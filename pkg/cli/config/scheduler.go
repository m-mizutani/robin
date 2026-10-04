package config

import (
	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"
)

// Scheduler holds how the scheduler runs due jobs. The flag names carry no
// command name, so any command that runs the scheduler takes the same flags.
type Scheduler struct {
	concurrency int64
}

func (x *Scheduler) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.Int64Flag{
			Name:        "concurrency",
			Category:    "Scheduler",
			Usage:       "Number of jobs run at the same time",
			Value:       4,
			Sources:     cli.EnvVars("ROBIN_SCHEDULE_CONCURRENCY"),
			Destination: &x.concurrency,
		},
	}
}

func (x *Scheduler) Validate() error {
	if x.concurrency < 1 {
		return goerr.New("--concurrency must be at least 1", goerr.V("concurrency", x.concurrency))
	}
	return nil
}

func (x *Scheduler) Concurrency() int { return int(x.concurrency) }
