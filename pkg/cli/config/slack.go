package config

import (
	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	slackadapter "github.com/m-mizutani/robin/pkg/adapter/slack"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// SlackApp holds the settings of signing in with Slack and receiving its
// events: the OAuth client, the signing secret and the workspace.
type SlackApp struct {
	clientID      string
	clientSecret  string
	signingSecret string
	teamID        string
}

func (x *SlackApp) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "slack-client-id",
			Category:    "Slack",
			Usage:       "Client ID of the Slack app",
			Sources:     cli.EnvVars("ROBIN_SLACK_CLIENT_ID"),
			Destination: &x.clientID,
		},
		&cli.StringFlag{
			Name:        "slack-client-secret",
			Category:    "Slack",
			Usage:       "Client secret of the Slack app",
			Sources:     cli.EnvVars("ROBIN_SLACK_CLIENT_SECRET"),
			Destination: &x.clientSecret,
		},
		&cli.StringFlag{
			Name:        "slack-signing-secret",
			Category:    "Slack",
			Usage:       "Signing secret of the Slack app, used to verify Events API requests",
			Sources:     cli.EnvVars("ROBIN_SLACK_SIGNING_SECRET"),
			Destination: &x.signingSecret,
		},
		&cli.StringFlag{
			Name:        "slack-team-id",
			Category:    "Slack",
			Usage:       "ID of the Slack workspace (T...) this server accepts",
			Sources:     cli.EnvVars("ROBIN_SLACK_TEAM_ID"),
			Destination: &x.teamID,
		},
	}
}

// Validate requires the client ID, the client secret, the signing secret and
// the team ID.
func (x *SlackApp) Validate() error {
	required := []struct {
		flag  string
		value string
	}{
		{"--slack-client-id", x.clientID},
		{"--slack-client-secret", x.clientSecret},
		{"--slack-signing-secret", x.signingSecret},
		{"--slack-team-id", x.teamID},
	}
	for _, r := range required {
		if r.value == "" {
			return goerr.New(r.flag + " is required")
		}
	}
	return x.validateTeamID()
}

// ValidateForNoAuth is the check used with --no-auth: only the workspace is
// required, since nobody signs in through Slack.
func (x *SlackApp) ValidateForNoAuth() error {
	if x.teamID == "" {
		return goerr.New("--slack-team-id is required")
	}
	return x.validateTeamID()
}

func (x *SlackApp) validateTeamID() error {
	if err := model.SlackTeamID(x.teamID).Validate(); err != nil {
		return goerr.Wrap(err, "invalid --slack-team-id")
	}
	return nil
}

func (x *SlackApp) ClientID() string          { return x.clientID }
func (x *SlackApp) ClientSecret() string      { return x.clientSecret }
func (x *SlackApp) SigningSecret() string     { return x.signingSecret }
func (x *SlackApp) TeamID() model.SlackTeamID { return model.SlackTeamID(x.teamID) }

// SlackBot holds the bot token. Every command that posts as Robin builds its
// bot here.
type SlackBot struct {
	botToken string
}

func (x *SlackBot) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "slack-bot-token",
			Category:    "Slack",
			Usage:       "Bot user OAuth token (xoxb-...)",
			Sources:     cli.EnvVars("ROBIN_SLACK_BOT_TOKEN"),
			Destination: &x.botToken,
		},
	}
}

// Validate makes the bot token mandatory when required.
func (x *SlackBot) Validate(required bool) error {
	if required && x.botToken == "" {
		return goerr.New("--slack-bot-token is required")
	}
	return nil
}

// Enabled reports whether a bot token is set.
func (x *SlackBot) Enabled() bool { return x.botToken != "" }

// Configure returns the bot, or nil when no bot token is set.
func (x *SlackBot) Configure() interfaces.SlackBot {
	if !x.Enabled() {
		return nil
	}
	return slackadapter.NewBot(x.botToken)
}
