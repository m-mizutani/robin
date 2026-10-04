package mention

import (
	"time"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

// SetClockForTest replaces the clock and the ID generator of Agent.
func (a *Agent) SetClockForTest(now func() time.Time, newID func() string) {
	a.now = now
	a.newID = newID
}

// BudgetVerdictForTest adds usages to a meter and names its verdict.
func BudgetVerdictForTest(rate model.Rate, limit model.NanoUSD, ratio float64, maxCalls int, usages ...model.LLMUsage) string {
	m := &budgetMeter{rate: rate, limit: limit, noticeRatio: ratio, maxCalls: maxCalls}
	for _, u := range usages {
		m.add(u)
	}
	switch m.verdict() {
	case budgetContinue:
		return "continue"
	case budgetConclude:
		return "conclude"
	default:
		return "exhausted"
	}
}
