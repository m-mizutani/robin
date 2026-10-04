package config

import (
	"net/url"

	"github.com/m-mizutani/goerr/v2"
	"github.com/slack-go/slack"
	"github.com/urfave/cli/v3"

	slackadapter "github.com/m-mizutani/robin/pkg/adapter/slack"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// defaultSlackAPIURL is the Web API base URL of slack-go.
const defaultSlackAPIURL = slack.APIURL

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

// SlackBot holds the bot token and the Web API the bot calls. Every command
// that posts as Robin builds its bot here.
type SlackBot struct {
	botToken string
	apiURL   string
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
		&cli.StringFlag{
			Name:        "slack-api-url",
			Category:    "Development",
			Usage:       "Base URL of the Slack Web API the bot calls. Values other than the default are accepted only with --no-auth",
			Value:       defaultSlackAPIURL,
			Sources:     cli.EnvVars("ROBIN_SLACK_API_URL"),
			Destination: &x.apiURL,
		},
	}
}

// Validate checks --slack-api-url: an http(s) URL, and other than the default
// only when noAuth is true. required makes the bot token mandatory.
func (x *SlackBot) Validate(required, noAuth bool) error {
	if required && x.botToken == "" {
		return goerr.New("--slack-bot-token is required")
	}
	u, err := url.Parse(x.apiURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return goerr.New("invalid --slack-api-url", goerr.V("slack_api_url", x.apiURL))
	}
	if x.apiURL != defaultSlackAPIURL && !noAuth {
		return goerr.New("--slack-api-url can be changed only with --no-auth", goerr.V("slack_api_url", x.apiURL))
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
	return slackadapter.NewBot(x.botToken, slack.OptionAPIURL(x.apiURL))
}
