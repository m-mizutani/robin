package config_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/cli/config"
)

func TestScheduler(t *testing.T) {
	unsetEnv(t, "ROBIN_SCHEDULE_CONCURRENCY")

	t.Run("default", func(t *testing.T) {
		var s config.Scheduler
		parse(t, s.Flags())
		gt.NoError(t, s.Validate())
		gt.Number(t, s.Concurrency()).Equal(4)
	})

	t.Run("value", func(t *testing.T) {
		var s config.Scheduler
		parse(t, s.Flags(), "--concurrency", "8")
		gt.NoError(t, s.Validate())
		gt.Number(t, s.Concurrency()).Equal(8)
	})

	t.Run("zero", func(t *testing.T) {
		var s config.Scheduler
		parse(t, s.Flags(), "--concurrency", "0")
		gt.Error(t, s.Validate())
	})
}
