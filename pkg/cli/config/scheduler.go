package config

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"
)

// Scheduler holds how the scheduler runs due jobs. The flag names carry no
// command name, so any command that runs the scheduler takes the same flags.
type Scheduler struct {
	maxDelay    time.Duration
	concurrency int64
}

func (x *Scheduler) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.DurationFlag{
			Name:        "max-delay",
			Category:    "Scheduler",
			Usage:       "Skip a scheduled run when the scheduler starts it later than this after its time",
			Value:       time.Hour,
			Sources:     cli.EnvVars("ROBIN_SCHEDULE_MAX_DELAY"),
			Destination: &x.maxDelay,
		},
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
	if x.maxDelay <= 0 {
		return goerr.New("--max-delay must be positive", goerr.V("max_delay", x.maxDelay))
	}
	if x.concurrency < 1 {
		return goerr.New("--concurrency must be at least 1", goerr.V("concurrency", x.concurrency))
	}
	return nil
}

func (x *Scheduler) MaxDelay() time.Duration { return x.maxDelay }
func (x *Scheduler) Concurrency() int        { return int(x.concurrency) }
