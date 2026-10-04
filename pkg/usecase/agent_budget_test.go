package usecase_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
)

func TestBudgetMeter(t *testing.T) {
	// One input token costs one nano dollar, so the token count is the cost.
	rate := model.Rate{Input: 1, Output: 1}
	limit := model.FromUSD(1)
	cents := func(c int64) model.LLMUsage { return model.LLMUsage{InputTokens: c * 10_000_000} }

	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 20), "continue")
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 20, cents(89)), "continue")
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 20, cents(90)), "conclude")
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 20, cents(50), cents(49)), "conclude")
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 20, cents(100)), "exhausted")
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 20, cents(120)), "exhausted")

	// The last allowed call is the concluding one.
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 3, cents(0)), "continue")
	gt.Equal(t, usecase.BudgetVerdictForTest(rate, limit, 0.9, 3, cents(0), cents(0)), "conclude")
}
