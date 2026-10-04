package config

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/vertex"
	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2/google"
)

// vertexScope is the OAuth scope of Vertex AI requests.
const vertexScope = "https://www.googleapis.com/auth/cloud-platform"

// LLM selects how Claude is called: through Vertex AI with Application
// Default Credentials, or through the Claude API with an API key. Exactly one
// of them is required when Slack events are enabled.
type LLM struct {
	vertexProjectID string
	vertexRegion    string
	apiKey          string
}

func (x *LLM) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "llm-vertex-project-id",
			Category:    "LLM",
			Usage:       "Google Cloud project to call Claude through Vertex AI (Application Default Credentials)",
			Sources:     cli.EnvVars("ROBIN_LLM_VERTEX_PROJECT_ID"),
			Destination: &x.vertexProjectID,
		},
		&cli.StringFlag{
			Name:        "llm-vertex-region",
			Category:    "LLM",
			Usage:       "Vertex AI region of Claude",
			Value:       "global",
			Sources:     cli.EnvVars("ROBIN_LLM_VERTEX_REGION"),
			Destination: &x.vertexRegion,
		},
		&cli.StringFlag{
			Name:        "anthropic-api-key",
			Category:    "LLM",
			Usage:       "API key to call the Claude API directly",
			Sources:     cli.EnvVars("ROBIN_ANTHROPIC_API_KEY"),
			Destination: &x.apiKey,
		},
	}
}

// Validate requires exactly one endpoint when the Slack events, and so the
// agent, are enabled. Setting both is an error in any case.
func (x *LLM) Validate(eventsEnabled bool) error {
	vertexSet := x.vertexProjectID != ""
	apiKeySet := x.apiKey != ""
	if vertexSet && apiKeySet {
		return goerr.New("set only one of --llm-vertex-project-id and --anthropic-api-key")
	}
	if eventsEnabled && !vertexSet && !apiKeySet {
		return goerr.New("--llm-vertex-project-id or --anthropic-api-key is required to answer Slack mentions")
	}
	if vertexSet && x.vertexRegion == "" {
		return goerr.New("--llm-vertex-region must not be empty")
	}
	return nil
}

// Configure returns the request options that select the endpoint and the
// credential. Missing Application Default Credentials are an error here
// instead of a panic in the SDK.
func (x *LLM) Configure(ctx context.Context) ([]option.RequestOption, error) {
	if x.apiKey != "" {
		return []option.RequestOption{option.WithAPIKey(x.apiKey)}, nil
	}
	creds, err := google.FindDefaultCredentials(ctx, vertexScope)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to find Google credentials for Vertex AI",
			goerr.V("project_id", x.vertexProjectID))
	}
	return []option.RequestOption{vertex.WithCredentials(ctx, x.vertexRegion, x.vertexProjectID, creds)}, nil
}

// UsesVertex reports whether Claude is called through Vertex AI.
func (x *LLM) UsesVertex() bool { return x.vertexProjectID != "" }
