package usecase

import (
	"context"
	"time"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

func (uc *AuthUseCase) SetNowForTest(now func() time.Time) { uc.now = now }

func (uc *SlackEventUseCase) SetNowForTest(now func() time.Time) { uc.now = now }

var TokenAADForTest = tokenAAD

func (uc *GoogleWorkspaceUseCase) SetNowForTest(now func() time.Time) { uc.now = now }

var GoogleTokenAADForTest = googleTokenAAD

func (a *NotionAccess) SetNowForTest(now func() time.Time) { a.now = now }

var NotionTokenAADForTest = notionTokenAAD

var (
	GitHubAccessTokenAADForTest  = githubAccessTokenAAD
	GitHubRefreshTokenAADForTest = githubRefreshTokenAAD
)

// SetClockForTest replaces the clock and the ID generator of Agent.
func (a *Agent) SetClockForTest(now func() time.Time, newID func() string) {
	a.now = now
	a.newID = newID
}

var StartPhrasesForTest = startPhrases

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

// SetClockForTest replaces the clock, the wait between polls, and the ID
// generator of GitHubUserAccess.
func (a *GitHubUserAccess) SetClockForTest(now func() time.Time, sleep func(ctx context.Context, d time.Duration) error, newID func() string) {
	a.now = now
	a.sleep = sleep
	a.newID = newID
}
