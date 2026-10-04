package config_test

import (
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/cli/config"
)

func TestScheduler(t *testing.T) {
	unsetEnv(t, "ROBIN_SCHEDULE_MAX_DELAY", "ROBIN_SCHEDULE_CONCURRENCY")

	t.Run("defaults", func(t *testing.T) {
		var s config.Scheduler
		parse(t, s.Flags())
		gt.NoError(t, s.Validate())
		gt.Value(t, s.MaxDelay()).Equal(time.Hour)
		gt.Number(t, s.Concurrency()).Equal(4)
	})

	t.Run("values", func(t *testing.T) {
		var s config.Scheduler
		parse(t, s.Flags(), "--max-delay", "30m", "--concurrency", "8")
		gt.NoError(t, s.Validate())
		gt.Value(t, s.MaxDelay()).Equal(30 * time.Minute)
		gt.Number(t, s.Concurrency()).Equal(8)
	})

	for name, args := range map[string][]string{
		"zero max delay":     {"--max-delay", "0s"},
		"negative max delay": {"--max-delay", "-1m"},
		"zero concurrency":   {"--concurrency", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			var s config.Scheduler
			parse(t, s.Flags(), args...)
			gt.Error(t, s.Validate())
		})
	}
}
