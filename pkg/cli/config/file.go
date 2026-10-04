package config

import (
	"context"
	"os"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/safe"
)

// Defaults of the settings file. The prices are those of Claude Sonnet 5.5 on
// the Claude API; a deployment on Vertex AI writes its own prices.
const (
	defaultLLMProvider        = "claude"
	defaultLLMModel           = "claude-sonnet-5-5"
	defaultInputUSDPerMTok    = 2.0
	defaultOutputUSDPerMTok   = 10.0
	defaultCacheReadPerMTok   = 0.2
	defaultCacheWritePerMTok  = 2.5
	defaultAgentBudgetUSD     = 2.0
	defaultAgentSessionTTLStr = "720h"
)

// File is the TOML settings file given by --config. Without the flag, every
// setting takes its default.
type File struct {
	path string
}

func (x *File) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "config",
			Usage:       "Path of the TOML settings file (model, prices, budget, session TTL)",
			Sources:     cli.EnvVars("ROBIN_CONFIG"),
			Destination: &x.path,
		},
	}
}

type fileContent struct {
	LLM   *llmSection   `toml:"llm"`
	Agent *agentSection `toml:"agent"`
}

type llmSection struct {
	Provider             *string  `toml:"provider"`
	Model                *string  `toml:"model"`
	InputUSDPerMTok      *float64 `toml:"input_usd_per_mtok"`
	OutputUSDPerMTok     *float64 `toml:"output_usd_per_mtok"`
	CacheReadUSDPerMTok  *float64 `toml:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok *float64 `toml:"cache_write_usd_per_mtok"`
}

type agentSection struct {
	BudgetUSD  *float64 `toml:"budget_usd"`
	SessionTTL *string  `toml:"session_ttl"`
}

// Settings are the values of the settings file after defaults are applied.
type Settings struct {
	Provider   string
	Model      string
	Rate       model.Rate
	Budget     model.NanoUSD
	SessionTTL time.Duration
}

// Load reads the settings file, or returns the defaults when no path is
// given. An unreadable file, invalid TOML, an unknown key or an out of range
// value is an error.
func (x *File) Load(ctx context.Context) (*Settings, error) {
	var content fileContent
	if x.path != "" {
		f, err := os.Open(x.path)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to open the settings file", goerr.V("path", x.path))
		}
		defer safe.Close(ctx, f)
		if err := toml.NewDecoder(f).DisallowUnknownFields().Decode(&content); err != nil {
			return nil, goerr.Wrap(err, "failed to read the settings file", goerr.V("path", x.path))
		}
	}

	llm, err := content.LLM.resolve()
	if err != nil {
		return nil, goerr.Wrap(err, "invalid [llm] in the settings file", goerr.V("path", x.path))
	}
	if err := content.Agent.resolve(llm); err != nil {
		return nil, goerr.Wrap(err, "invalid [agent] in the settings file", goerr.V("path", x.path))
	}
	return llm, nil
}

func (s *llmSection) resolve() (*Settings, error) {
	out := &Settings{
		Provider: defaultLLMProvider,
		Model:    defaultLLMModel,
		Rate: model.Rate{
			Input:      model.FromUSDPerMTok(defaultInputUSDPerMTok),
			Output:     model.FromUSDPerMTok(defaultOutputUSDPerMTok),
			CacheRead:  model.FromUSDPerMTok(defaultCacheReadPerMTok),
			CacheWrite: model.FromUSDPerMTok(defaultCacheWritePerMTok),
		},
	}
	if s == nil {
		return out, nil
	}

	if s.Provider != nil {
		if *s.Provider != "claude" {
			return nil, goerr.New(`llm.provider supports only "claude"`, goerr.V("provider", *s.Provider))
		}
		out.Provider = *s.Provider
	}

	prices := []*float64{s.InputUSDPerMTok, s.OutputUSDPerMTok, s.CacheReadUSDPerMTok, s.CacheWriteUSDPerMTok}
	anyPrice := false
	for _, p := range prices {
		anyPrice = anyPrice || p != nil
	}
	// The model and its prices change together: a model with the prices of
	// another one would measure the spending wrongly.
	if s.Provider == nil && s.Model == nil && !anyPrice {
		return out, nil
	}
	if s.Model == nil || *s.Model == "" {
		return nil, goerr.New("llm.model is required when llm.provider or a price is written")
	}
	for _, p := range prices {
		if p == nil {
			return nil, goerr.New("llm.model needs all four prices: input_usd_per_mtok, output_usd_per_mtok, cache_read_usd_per_mtok and cache_write_usd_per_mtok")
		}
	}
	if *s.InputUSDPerMTok <= 0 || *s.OutputUSDPerMTok <= 0 {
		return nil, goerr.New("input and output prices must be positive")
	}
	if *s.CacheReadUSDPerMTok < 0 || *s.CacheWriteUSDPerMTok < 0 {
		return nil, goerr.New("cache prices must not be negative")
	}
	out.Model = *s.Model
	out.Rate = model.Rate{
		Input:      model.FromUSDPerMTok(*s.InputUSDPerMTok),
		Output:     model.FromUSDPerMTok(*s.OutputUSDPerMTok),
		CacheRead:  model.FromUSDPerMTok(*s.CacheReadUSDPerMTok),
		CacheWrite: model.FromUSDPerMTok(*s.CacheWriteUSDPerMTok),
	}
	if err := out.Rate.Validate(); err != nil {
		return nil, goerr.Wrap(err, "prices are too small to count per token")
	}
	return out, nil
}

func (s *agentSection) resolve(out *Settings) error {
	budget := defaultAgentBudgetUSD
	ttl := defaultAgentSessionTTLStr
	if s != nil {
		if s.BudgetUSD != nil {
			budget = *s.BudgetUSD
		}
		if s.SessionTTL != nil {
			ttl = *s.SessionTTL
		}
	}
	if budget <= 0 {
		return goerr.New("agent.budget_usd must be positive", goerr.V("budget_usd", budget))
	}
	d, err := time.ParseDuration(ttl)
	if err != nil {
		return goerr.Wrap(err, "agent.session_ttl is not a duration", goerr.V("session_ttl", ttl))
	}
	if d <= 0 {
		return goerr.New("agent.session_ttl must be positive", goerr.V("session_ttl", ttl))
	}
	out.Budget = model.FromUSD(budget)
	out.SessionTTL = d
	return nil
}
