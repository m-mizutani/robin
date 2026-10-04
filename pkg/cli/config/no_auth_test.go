package config_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

func TestNoAuth(t *testing.T) {
	unsetEnv(t, "ROBIN_NO_AUTH")

	t.Run("disabled by default", func(t *testing.T) {
		var n config.NoAuth
		parse(t, n.Flags())
		gt.Bool(t, n.Enabled()).False()
		gt.NoError(t, n.Validate())
	})

	t.Run("user ID", func(t *testing.T) {
		var n config.NoAuth
		parse(t, n.Flags(), "--no-auth", "U0E2ETEST")
		gt.Bool(t, n.Enabled()).True()
		gt.NoError(t, n.Validate())
		gt.Value(t, n.UserID()).Equal(model.SlackUserID("U0E2ETEST"))
	})

	t.Run("invalid user ID", func(t *testing.T) {
		var n config.NoAuth
		parse(t, n.Flags(), "--no-auth", "alice")
		gt.Error(t, n.Validate())
	})
}

func TestSlackApp_ValidateForNoAuth(t *testing.T) {
	clearSlackEnv(t)

	t.Run("team ID only", func(t *testing.T) {
		var s config.SlackApp
		parse(t, s.Flags(), "--slack-team-id", "T0123ABCD")
		gt.NoError(t, s.ValidateForNoAuth())
	})

	t.Run("missing team ID", func(t *testing.T) {
		var s config.SlackApp
		parse(t, s.Flags())
		gt.Error(t, s.ValidateForNoAuth())
	})

	t.Run("invalid team ID", func(t *testing.T) {
		var s config.SlackApp
		parse(t, s.Flags(), "--slack-team-id", "U0123ABCD")
		gt.Error(t, s.ValidateForNoAuth())
	})
}
