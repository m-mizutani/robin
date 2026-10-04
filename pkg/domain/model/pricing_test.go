package model_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

func sonnetRate() model.Rate {
	return model.Rate{
		Input:      model.FromUSDPerMTok(2),
		Output:     model.FromUSDPerMTok(10),
		CacheRead:  model.FromUSDPerMTok(0.2),
		CacheWrite: model.FromUSDPerMTok(2.5),
	}
}

func TestRate_Cost(t *testing.T) {
	cost := sonnetRate().Cost(model.LLMUsage{
		InputTokens:              1000,
		OutputTokens:             500,
		CacheReadInputTokens:     2000,
		CacheCreationInputTokens: 300,
	})
	gt.Equal(t, cost, model.NanoUSD(2_000_000+5_000_000+400_000+750_000))
}

func TestNanoUSD(t *testing.T) {
	gt.Equal(t, model.NanoUSD(2_005_000_000).USD(), "$2.01")
	gt.Equal(t, model.NanoUSD(0).USD(), "$0.00")
	gt.Equal(t, model.NanoUSD(1_994_000_000).USD(), "$1.99")
	gt.Equal(t, model.FromUSD(2), model.NanoUSD(2_000_000_000))
	gt.Equal(t, model.FromUSDPerMTok(2.5), model.NanoUSD(2_500))
	gt.Equal(t, model.FromUSDPerMTok(0.2), model.NanoUSD(200))
}

func TestRate_Validate(t *testing.T) {
	gt.NoError(t, sonnetRate().Validate())
	gt.NoError(t, model.Rate{Input: 1, Output: 1}.Validate())

	cases := map[string]model.Rate{
		"zero input":           {Output: 1},
		"zero output":          {Input: 1},
		"negative cache read":  {Input: 1, Output: 1, CacheRead: -1},
		"negative cache write": {Input: 1, Output: 1, CacheWrite: -1},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			gt.Error(t, r.Validate())
		})
	}
}
