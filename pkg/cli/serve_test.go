package cli_test

import (
	"context"
	"os"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/cli"
)

func clearServeEnv(t *testing.T) {
	t.Helper()
	// Unset rather than set to "": urfave/cli takes an empty variable as the
	// flag value and skips the default.
	for _, name := range []string{
		"ROBIN_ADDR", "ROBIN_BASE_URL", "ROBIN_SESSION_TTL", "ROBIN_LOG_LEVEL", "ROBIN_LOG_FORMAT",
		"ROBIN_REPOSITORY_BACKEND", "ROBIN_FIRESTORE_PROJECT_ID", "ROBIN_FIRESTORE_DATABASE_ID",
		"ROBIN_SLACK_CLIENT_ID", "ROBIN_SLACK_CLIENT_SECRET", "ROBIN_SLACK_SIGNING_SECRET",
		"ROBIN_SLACK_BOT_TOKEN", "ROBIN_SLACK_TEAM_ID", "ROBIN_KMS_KEY_NAME", "ROBIN_NO_AUTH",
		"ROBIN_GOOGLE_CLIENT_ID", "ROBIN_GOOGLE_CLIENT_SECRET",
		"ROBIN_NOTION_CLIENT_ID", "ROBIN_NOTION_CLIENT_SECRET", "ROBIN_NOTION_WORKSPACE_ID", "ROBIN_NOTION_API_URL",
		"ROBIN_GITHUB_CLIENT_ID", "ROBIN_GITHUB_CLIENT_SECRET",
		"ROBIN_CONFIG", "ROBIN_LLM_VERTEX_PROJECT_ID", "ROBIN_LLM_VERTEX_REGION", "ROBIN_ANTHROPIC_API_KEY",
	} {
		t.Setenv(name, "")
		gt.NoError(t, os.Unsetenv(name)).Required()
	}
}

func validServeArgs() []string {
	return []string{
		"robin", "--log-format", "json", "serve",
		"--base-url", "https://robin.example.com",
		"--repository-backend", "memory",
		"--slack-client-id", "client-id",
		"--slack-client-secret", "client-secret",
		"--slack-signing-secret", "signing-secret",
		"--slack-bot-token", "xoxb-token",
		"--slack-team-id", "T0123ABCD",
		"--kms-key-name", "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		"--anthropic-api-key", "sk-ant-test",
	}
}

func without(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// Each required setting stops the command before anything is connected, and
// the error names the missing flag.
func TestServe_RequiredFlags(t *testing.T) {
	clearServeEnv(t)
	for _, flag := range []string{
		"--base-url",
		"--slack-client-id",
		"--slack-client-secret",
		"--slack-signing-secret",
		"--slack-bot-token",
		"--slack-team-id",
		"--kms-key-name",
		"--anthropic-api-key",
	} {
		t.Run(flag, func(t *testing.T) {
			err := cli.Run(context.Background(), without(validServeArgs(), flag), "test")
			gt.Value(t, err).NotNil().Required()
			gt.String(t, err.Error()).Contains(flag)
		})
	}
}

func TestServe_FirestoreRequiresProjectID(t *testing.T) {
	clearServeEnv(t)
	args := without(validServeArgs(), "--repository-backend")
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("--firestore-project-id")
}

func TestServe_NoAuthRequiresMemoryBackend(t *testing.T) {
	clearServeEnv(t)
	args := []string{
		"robin", "--log-format", "json", "serve",
		"--base-url", "http://localhost:8080",
		"--repository-backend", "firestore", "--firestore-project-id", "my-project",
		"--slack-team-id", "T0123ABCD",
		"--no-auth", "U0E2ETEST",
	}
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("--no-auth requires --repository-backend memory")
}

func TestServe_NoAuthNeedsTeamID(t *testing.T) {
	clearServeEnv(t)
	args := []string{
		"robin", "--log-format", "json", "serve",
		"--base-url", "http://localhost:8080",
		"--repository-backend", "memory",
		"--no-auth", "U0E2ETEST",
	}
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("--slack-team-id")
}

func noAuthGoogleArgs() []string {
	return []string{
		"robin", "--log-format", "json", "serve",
		// An address that cannot be listened on stops the server right after
		// the configuration has been validated and assembled.
		"--addr", "127.0.0.1:-1",
		"--base-url", "http://localhost:8080",
		"--repository-backend", "memory",
		"--slack-team-id", "T0123ABCD",
		"--no-auth", "U0E2ETEST",
		"--google-client-id", "client-id",
		"--google-client-secret", "client-secret",
	}
}

func TestServe_NoAuthAcceptsGoogle(t *testing.T) {
	clearServeEnv(t)
	err := cli.Run(context.Background(), noAuthGoogleArgs(), "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("HTTP server stopped")
}

func TestServe_GoogleNeedsBothFlags(t *testing.T) {
	clearServeEnv(t)
	for _, flag := range []string{"--google-client-id", "--google-client-secret"} {
		t.Run("without "+flag, func(t *testing.T) {
			for _, args := range [][]string{without(noAuthGoogleArgs(), flag), without(append(validServeArgs(), "--google-client-id", "client-id", "--google-client-secret", "client-secret"), flag)} {
				err := cli.Run(context.Background(), args, "test")
				gt.Value(t, err).NotNil().Required()
				gt.String(t, err.Error()).Contains("--google-client-id and --google-client-secret must be set together")
			}
		})
	}
}

var notionFlags = []string{
	"--notion-client-id", "client-id",
	"--notion-client-secret", "client-secret",
	"--notion-workspace-id", "0f4a2b1c-3d4e-4f50-8a6b-7c8d9e0f1a2b",
}

// With --no-auth and without KMS, the Notion integration is assembled with the
// in-process cipher and a fake Notion API URL.
func TestServe_NoAuthAcceptsNotion(t *testing.T) {
	clearServeEnv(t)
	args := append(append(noAuthGoogleArgs(), notionFlags...), "--notion-api-url", "http://127.0.0.1:18082")
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("HTTP server stopped")
}

func TestServe_NotionNeedsAllFlags(t *testing.T) {
	clearServeEnv(t)
	for _, flag := range []string{"--notion-client-id", "--notion-client-secret", "--notion-workspace-id"} {
		t.Run("without "+flag, func(t *testing.T) {
			for _, base := range [][]string{noAuthGoogleArgs(), validServeArgs()} {
				err := cli.Run(context.Background(), without(append(base, notionFlags...), flag), "test")
				gt.Value(t, err).NotNil().Required()
				gt.String(t, err.Error()).Contains("--notion-client-id, --notion-client-secret, and --notion-workspace-id must be set together")
			}
		})
	}
}

func TestServe_NotionAPIURLNeedsNoAuth(t *testing.T) {
	clearServeEnv(t)
	args := append(append(validServeArgs(), notionFlags...), "--notion-api-url", "http://127.0.0.1:18082")
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("--notion-api-url can be changed only with --no-auth")
}

func noAuthGitHubArgs() []string {
	args := without(without(noAuthGoogleArgs(), "--google-client-id"), "--google-client-secret")
	return append(args, "--github-client-id", "Iv1.client", "--github-client-secret", "client-secret")
}

func TestServe_NoAuthAcceptsGitHub(t *testing.T) {
	clearServeEnv(t)
	err := cli.Run(context.Background(), noAuthGitHubArgs(), "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("HTTP server stopped")
}

func TestServe_GitHubNeedsBothFlags(t *testing.T) {
	clearServeEnv(t)
	for _, flag := range []string{"--github-client-id", "--github-client-secret"} {
		t.Run("without "+flag, func(t *testing.T) {
			for _, args := range [][]string{without(noAuthGitHubArgs(), flag), without(append(validServeArgs(), "--github-client-id", "Iv1.client", "--github-client-secret", "client-secret"), flag)} {
				err := cli.Run(context.Background(), args, "test")
				gt.Value(t, err).NotNil().Required()
				gt.String(t, err.Error()).Contains("--github-client-id and --github-client-secret must be set together")
			}
		})
	}
}

// With Slack events, the agent is assembled with the LLM endpoint and the
// settings file before the server starts.
func TestServe_NoAuthAcceptsSlackAgent(t *testing.T) {
	clearServeEnv(t)
	args := append(noAuthGitHubArgs(),
		"--slack-bot-token", "xoxb-token",
		"--slack-signing-secret", "signing-secret",
		"--anthropic-api-key", "sk-ant-test",
		"--config", "config/testdata/full.toml",
	)
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("HTTP server stopped")
}

func TestServe_SlackAgentNeedsLLM(t *testing.T) {
	clearServeEnv(t)
	args := append(noAuthGitHubArgs(), "--slack-bot-token", "xoxb-token", "--slack-signing-secret", "signing-secret")
	err := cli.Run(context.Background(), args, "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("--llm-vertex-project-id or --anthropic-api-key is required")

	err = cli.Run(context.Background(), append(validServeArgs(), "--llm-vertex-project-id", "my-project"), "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("set only one of --llm-vertex-project-id and --anthropic-api-key")
}

func TestServe_InvalidSettingsFile(t *testing.T) {
	clearServeEnv(t)
	err := cli.Run(context.Background(), append(noAuthGitHubArgs(), "--config", "config/testdata/unknown_key.toml"), "test")
	gt.Value(t, err).NotNil().Required()
	gt.String(t, err.Error()).Contains("failed to read the settings file")
}

func TestRun_InvalidLogLevel(t *testing.T) {
	clearServeEnv(t)
	err := cli.Run(context.Background(), []string{"robin", "--log-level", "verbose", "serve"}, "test")
	gt.Value(t, err).NotNil()
}
