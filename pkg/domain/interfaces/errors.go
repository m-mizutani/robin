package interfaces

import "errors"

var (
	// ErrNotFound is returned by a repository when the requested entity does
	// not exist.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned by a repository create operation when an
	// entity with the same ID already exists.
	ErrAlreadyExists = errors.New("already exists")

	// ErrKeyMismatch is returned when an entity's user key differs from the
	// key the caller passed. It guards the per-user document path.
	ErrKeyMismatch = errors.New("entity key does not match the requested user key")

	// ErrSlackTokenInvalid is returned when Slack reports that a token can no
	// longer be used (revoked, expired, account deactivated).
	ErrSlackTokenInvalid = errors.New("slack token is invalid")

	// ErrGoogleTokenInvalid is returned when Google rejects a token as invalid
	// (already revoked, expired, or unknown).
	ErrGoogleTokenInvalid = errors.New("google token is invalid")

	// ErrGoogleAccountInUse is returned when a Google account is already
	// connected to another user.
	ErrGoogleAccountInUse = errors.New("google account is connected to another user")

	// ErrNotionTokenInvalid is returned when Notion rejects an access token
	// (401 unauthorized) or a refresh token (400 invalid_grant).
	ErrNotionTokenInvalid = errors.New("notion token is invalid")

	// ErrNotionForbidden is returned when the token lacks the capability for
	// a request (403 restricted_resource).
	ErrNotionForbidden = errors.New("notion request is forbidden")

	// ErrNotionNotFound is returned when an object does not exist or is not
	// shared with the integration (404 object_not_found).
	ErrNotionNotFound = errors.New("notion object not found")

	// ErrNotionRateLimited is returned when Notion asks to slow down (429).
	ErrNotionRateLimited = errors.New("notion rate limit exceeded")

	// ErrNotionAccountInUse is returned when a Notion account is already
	// connected to another user.
	ErrNotionAccountInUse = errors.New("notion account is connected to another user")

	// ErrGitHubTokenInvalid is returned when GitHub rejects a token as
	// invalid (revoked, expired, already used, or unknown).
	ErrGitHubTokenInvalid = errors.New("github token is invalid")

	// ErrGitHubAccountInUse is returned when a GitHub account is already
	// connected to another user.
	ErrGitHubAccountInUse = errors.New("github account is connected to another user")

	// ErrGoogleNotFound is returned when Google answers 404.
	ErrGoogleNotFound = errors.New("google object not found")

	// ErrGoogleUnsupportedFile is returned for a Drive file that cannot be
	// turned into text.
	ErrGoogleUnsupportedFile = errors.New("google drive file cannot be read as text")

	// ErrGitHubNotFound is returned when GitHub answers 404.
	ErrGitHubNotFound = errors.New("github object not found")

	// ErrSlackMessageNotFound is returned when a Slack message does not exist
	// or Robin cannot read it.
	ErrSlackMessageNotFound = errors.New("slack message not found")

	// ErrSlackChannelNotFound is returned when a channel does not exist or
	// Robin cannot see it.
	ErrSlackChannelNotFound = errors.New("slack channel not found")

	// ErrJobLimitReached is returned by a repository when the user already has
	// the most jobs allowed.
	ErrJobLimitReached = errors.New("job limit reached")

	// ErrLLMHistoryIncompatible means the stored conversation was written by
	// another provider or format version and cannot be continued.
	ErrLLMHistoryIncompatible = errors.New("llm history format is not supported by this client")
)
