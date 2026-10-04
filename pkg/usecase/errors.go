package usecase

import "errors"

var (
	// ErrUnauthenticated means the web session is missing, unknown, expired,
	// or its secret does not match.
	ErrUnauthenticated = errors.New("unauthenticated")

	// ErrLoginRejected means Slack completed the authorization but the result
	// does not satisfy the login conditions (workspace, token type, scopes,
	// identity).
	ErrLoginRejected = errors.New("login rejected")

	// ErrSlackNotConnected means no Slack user token is stored for the user.
	ErrSlackNotConnected = errors.New("slack account is not connected")

	// ErrGoogleScopeNotGranted means the user did not allow every required
	// Google scope on the consent screen.
	ErrGoogleScopeNotGranted = errors.New("required google scope was not granted")

	// ErrGoogleConnectRejected means Google completed the authorization but
	// the result cannot be stored (no refresh token, no account identity).
	ErrGoogleConnectRejected = errors.New("google connection rejected")

	// ErrGoogleWorkspaceNotConnected means no Google refresh token is stored
	// for the user.
	ErrGoogleWorkspaceNotConnected = errors.New("google workspace is not connected")

	// ErrGoogleAccountInUse means the Google account the user authorized is
	// already connected to another user.
	ErrGoogleAccountInUse = errors.New("google account is connected to another user")

	// ErrGoogleWorkspaceAlreadyConnected means the user already has a
	// connected Google account; a new connection is ignored.
	ErrGoogleWorkspaceAlreadyConnected = errors.New("google workspace is already connected")

	// ErrNotionNotConnected means no Notion token is stored for the user.
	ErrNotionNotConnected = errors.New("notion is not connected")

	// ErrNotionReconnectRequired means Notion rejected the stored refresh
	// token; the user has to connect Notion again.
	ErrNotionReconnectRequired = errors.New("notion needs to be reconnected")

	// ErrNotionConnectRejected means Notion completed the authorization but
	// the result cannot be stored (not a user authorization, missing tokens).
	ErrNotionConnectRejected = errors.New("notion connection rejected")

	// ErrNotionWrongWorkspace means the user authorized a Notion workspace
	// other than the configured one.
	ErrNotionWrongWorkspace = errors.New("notion workspace is not the configured one")

	// ErrNotionAccountInUse means the Notion account the user authorized is
	// already connected to another user.
	ErrNotionAccountInUse = errors.New("notion account is connected to another user")

	// ErrNotionAlreadyConnected means the user already has a working Notion
	// connection; a new connection is ignored.
	ErrNotionAlreadyConnected = errors.New("notion is already connected")

	// ErrNotionInvalidRequest means a read request was rejected before it was
	// sent to Notion (malformed ID, page size out of range, invalid JSON).
	ErrNotionInvalidRequest = errors.New("invalid notion request")

	// ErrGitHubNotConnected means no usable GitHub connection is stored for
	// the user.
	ErrGitHubNotConnected = errors.New("github account is not connected")

	// ErrGitHubConnectRejected means GitHub completed the authorization but
	// the returned token or account cannot be stored.
	ErrGitHubConnectRejected = errors.New("github connection rejected")

	// ErrGitHubAccountInUse means the GitHub account the user authorized is
	// already connected to another user.
	ErrGitHubAccountInUse = errors.New("github account is connected to another user")

	// ErrGitHubAlreadyConnected means the user already has a connected GitHub
	// account; a new connection is ignored.
	ErrGitHubAlreadyConnected = errors.New("github is already connected")

	// ErrGitHubRefreshTimeout means another instance kept refreshing the
	// user's token for longer than the wait limit.
	ErrGitHubRefreshTimeout = errors.New("timed out waiting for the github token refresh")

	// ErrGoogleWorkspaceReconnectRequired means Google rejected the stored
	// refresh token; the user has to disconnect and connect again.
	ErrGoogleWorkspaceReconnectRequired = errors.New("google workspace needs to be reconnected")

	// ErrAgentSessionLeaseLost means another run took the session's lease
	// before this run committed.
	ErrAgentSessionLeaseLost = errors.New("agent session lease was lost")
)
