package usecase

import (
	"context"
	"time"
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

var StartPhrasesForTest = startPhrases

// SetClockForTest replaces the clock, the wait between polls, and the ID
// generator of GitHubUserAccess.
func (a *GitHubUserAccess) SetClockForTest(now func() time.Time, sleep func(ctx context.Context, d time.Duration) error, newID func() string) {
	a.now = now
	a.sleep = sleep
	a.newID = newID
}
