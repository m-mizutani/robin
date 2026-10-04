package interfaces

import (
	"context"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

// GitHubOAuth calls the OAuth endpoints of the GitHub App.
type GitHubOAuth interface {
	// AuthorizeURL returns the authorization URL with a PKCE challenge
	// derived from codeVerifier.
	AuthorizeURL(redirectURI, state, codeVerifier string) string
	ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*model.GitHubToken, error)
	// Refresh fails with ErrGitHubTokenInvalid when GitHub rejects the
	// refresh token.
	Refresh(ctx context.Context, refreshToken model.GitHubRefreshToken) (*model.GitHubToken, error)
	// RevokeGrant deletes the user's authorization of the app together with
	// every token of it. It fails with ErrGitHubTokenInvalid when GitHub does
	// not know the token.
	RevokeGrant(ctx context.Context, accessToken model.GitHubAccessToken) error
}

// GitHubUserClientFactory builds a client authenticated with one user's
// access token.
type GitHubUserClientFactory interface {
	New(token model.GitHubAccessToken) GitHubUserClient
}

// GitHubUserClient calls the GitHub REST API with one user's access token.
type GitHubUserClient interface {
	// GetUser fails with ErrGitHubTokenInvalid when GitHub answers 401.
	GetUser(ctx context.Context) (*model.GitHubIdentity, error)
	// The methods below fail with ErrGitHubTokenInvalid on 401 and
	// ErrGitHubNotFound on 404.
	SearchIssues(ctx context.Context, query string, perPage int) ([]model.GitHubIssueSummary, error)
	SearchCode(ctx context.Context, query string, perPage int) ([]model.GitHubCodeHit, error)
	GetIssue(ctx context.Context, owner, repo string, number int) (*model.GitHubIssue, error)
	GetContent(ctx context.Context, owner, repo, path, ref string) (*model.GitHubContent, error)
}
