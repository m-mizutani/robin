package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/cli/config"
)

func parseLLM(t *testing.T, args ...string) *config.LLM {
	t.Helper()
	unsetEnv(t, "ROBIN_LLM_VERTEX_PROJECT_ID", "ROBIN_LLM_VERTEX_REGION", "ROBIN_ANTHROPIC_API_KEY")
	var x config.LLM
	parse(t, x.Flags(), args...)
	return &x
}

func TestLLM_Validate(t *testing.T) {
	cases := map[string]struct {
		args    []string
		events  bool
		wantErr bool
	}{
		"vertex with events":       {args: []string{"--llm-vertex-project-id", "my-project"}, events: true},
		"api key with events":      {args: []string{"--anthropic-api-key", "sk-ant-secret"}, events: true},
		"both with events":         {args: []string{"--llm-vertex-project-id", "p", "--anthropic-api-key", "sk-ant-secret"}, events: true, wantErr: true},
		"neither with events":      {events: true, wantErr: true},
		"neither without events":   {events: false},
		"both without events":      {args: []string{"--llm-vertex-project-id", "p", "--anthropic-api-key", "sk-ant-secret"}, wantErr: true},
		"vertex with empty region": {args: []string{"--llm-vertex-project-id", "p", "--llm-vertex-region", ""}, events: true, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := parseLLM(t, tc.args...).Validate(tc.events)
			if !tc.wantErr {
				gt.NoError(t, err)
				return
			}
			gt.Error(t, err)
			gt.False(t, strings.Contains(err.Error(), "sk-ant-secret"))
		})
	}
}

func TestLLM_ConfigureAPIKey(t *testing.T) {
	opts, err := parseLLM(t, "--anthropic-api-key", "sk-ant-secret").Configure(context.Background())
	gt.NoError(t, err).Required()
	gt.Array(t, opts).Length(1)
}

func TestLLM_ConfigureVertex(t *testing.T) {
	// Authorized user credentials are read without calling Google.
	path := filepath.Join(t.TempDir(), "adc.json")
	gt.NoError(t, os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"id","client_secret":"secret","refresh_token":"refresh"}`), 0o600)).Required()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)

	x := parseLLM(t, "--llm-vertex-project-id", "my-project")
	gt.True(t, x.UsesVertex())
	opts, err := x.Configure(context.Background())
	gt.NoError(t, err).Required()
	gt.Array(t, opts).Length(1)
}

func TestLLM_ConfigureVertexWithoutCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	_, err := parseLLM(t, "--llm-vertex-project-id", "my-project").Configure(context.Background())
	gt.Error(t, err)
}

func TestLLM_DefaultRegion(t *testing.T) {
	// The region defaults to "global"; an empty value is rejected above, so a
	// project alone validates.
	gt.NoError(t, parseLLM(t, "--llm-vertex-project-id", "my-project").Validate(true))
}
