package interfaces

import (
	"context"
	"time"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/domain/model/auth"
)

// Repository is the persistence boundary. Every method that reads or writes
// data owned by a user takes that user's key, and no method returns data of
// more than one user.
type Repository interface {
	User() UserRepository
	SlackCredential() SlackCredentialRepository
	GoogleWorkspaceCredential() GoogleWorkspaceCredentialRepository
	NotionCredential() NotionCredentialRepository
	GitHubCredential() GitHubCredentialRepository
	Session() SessionRepository
	SlackEvent() SlackEventRepository
	AgentSession() AgentSessionRepository
	Job() JobRepository
	Close() error
}

// JobRepository keeps the jobs of each user under that user, and one schedule
// entry per job outside the user's document so the scheduler can find due
// jobs. The entry is written and deleted in the same transaction as its job.
type JobRepository interface {
	// Create stores job and its schedule entry atomically. It fails with
	// ErrAlreadyExists when the ID is taken and with ErrJobLimitReached when
	// key already has maxJobs jobs.
	Create(ctx context.Context, key model.UserKey, job *model.Job, maxJobs int) error
	// Get fails with ErrNotFound when key has no job of that ID.
	Get(ctx context.Context, key model.UserKey, id model.JobID) (*model.Job, error)
	// List returns key's jobs ordered by CreatedAt.
	List(ctx context.Context, key model.UserKey) ([]*model.Job, error)
	// Delete removes the job and its schedule entry. A missing job is not an
	// error. Run records are left to expire.
	Delete(ctx context.Context, key model.UserKey, id model.JobID) error
	// ListDue returns up to limit schedule entries of every user whose
	// NextRunAt is at or before now, oldest first. Only the scheduler calls it.
	ListDue(ctx context.Context, now time.Time, limit int) ([]*model.JobScheduleEntry, error)
	// Claim creates req.Run and moves the job to req.NextRunAt, in one
	// transaction, only while the job's NextRunAt equals req.ScheduledAt and
	// no run of that ID exists. It reports whether it wrote. A missing job is
	// not an error.
	Claim(ctx context.Context, key model.UserKey, req model.JobClaimRequest) (bool, error)
	// Finish replaces the run with run, which must be finished, and sets the
	// job's LastRun and UpdatedAt (= run.FinishedAt) while LastRun is the
	// same run. A missing job is not an error.
	Finish(ctx context.Context, key model.UserKey, run *model.JobRun) error
}

// AgentSessionRepository keeps the conversation of one Slack thread under the
// user who started it. The owner of a thread is recorded outside the user's
// document; it is written together with the session and never returned.
type AgentSessionRepository interface {
	// Begin starts a run of key on the thread, in one transaction. It returns
	// OwnedByOther when another user owns an unexpired session of the thread,
	// Busy when key's session holds an unexpired lease, Resumed with the lease
	// taken when key's session is unexpired, and Started after writing a new
	// session (new generation) and the owner record otherwise.
	Begin(ctx context.Context, key model.UserKey, req model.AgentSessionBeginRequest) (*model.AgentSessionBeginResult, error)
	// OwnedByOther reports whether a user other than key owns an unexpired
	// session of the thread.
	OwnedByOther(ctx context.Context, key model.UserKey, id model.AgentSessionID, now time.Time) (bool, error)
	// ListMessages returns the messages of the generation, ordered by Seq.
	ListMessages(ctx context.Context, key model.UserKey, id model.AgentSessionID, generation string) ([]*model.AgentSessionMessage, error)
	// Commit appends c.Messages, numbered from the stored MessageCount, updates
	// MessageCount, LastMentionTS and UpdatedAt, and clears the lease, only
	// while leaseID holds the lease. It reports whether it wrote.
	Commit(ctx context.Context, key model.UserKey, id model.AgentSessionID, leaseID string, c model.AgentSessionCommit) (bool, error)
	// Release clears the lease when leaseID holds it. A missing session is not an error.
	Release(ctx context.Context, key model.UserKey, id model.AgentSessionID, leaseID string) error
}

type UserRepository interface {
	Put(ctx context.Context, user *model.User) error
	Get(ctx context.Context, key model.UserKey) (*model.User, error)
}

type SlackCredentialRepository interface {
	Put(ctx context.Context, key model.UserKey, cred *model.SlackCredential) error
	Get(ctx context.Context, key model.UserKey) (*model.SlackCredential, error)
	// DeleteIfUnchanged removes the credential only while it still holds the
	// ciphertext of expected, so a credential replaced by a newer sign-in
	// survives. It reports whether a credential was deleted; a missing or
	// replaced credential is not an error.
	DeleteIfUnchanged(ctx context.Context, key model.UserKey, expected *model.SlackCredential) (bool, error)
}

// GoogleWorkspaceCredentialRepository keeps at most one credential per user
// and connects each Google account (by cred.Subject) to at most one user. The
// owner of a Google account is recorded outside the user's document; it is
// written and deleted together with the credential and never returned.
type GoogleWorkspaceCredentialRepository interface {
	// Create stores the credential and makes key the owner of its Google
	// account, atomically. It fails with ErrAlreadyExists when key already has
	// a credential, and with ErrGoogleAccountInUse when another user owns the
	// Google account.
	Create(ctx context.Context, key model.UserKey, cred *model.GoogleWorkspaceCredential) error
	Get(ctx context.Context, key model.UserKey) (*model.GoogleWorkspaceCredential, error)
	// AccountInUse reports whether a user other than key owns the Google
	// account identified by subject.
	AccountInUse(ctx context.Context, key model.UserKey, subject string) (bool, error)
	// DeleteIfUnchanged removes the credential, and the ownership of its
	// Google account, only while the credential still holds the ciphertext of
	// expected, so a credential stored by a later connection survives. It
	// reports whether a credential was deleted; a missing or replaced
	// credential is not an error.
	DeleteIfUnchanged(ctx context.Context, key model.UserKey, expected *model.GoogleWorkspaceCredential) (bool, error)
}

// NotionCredentialRepository keeps at most one credential per user and
// connects each Notion account (by cred.NotionUserID) to at most one user. The
// owner of a Notion account is recorded outside the user's document; it is
// written and deleted together with the credential and never returned.
type NotionCredentialRepository interface {
	// Create stores the credential and makes key the owner of its Notion
	// account, atomically. It fails with ErrAlreadyExists when key already has
	// a credential, and with ErrNotionAccountInUse when another user owns the
	// Notion account.
	Create(ctx context.Context, key model.UserKey, cred *model.NotionCredential) error
	Get(ctx context.Context, key model.UserKey) (*model.NotionCredential, error)
	// AccountInUse reports whether a user other than key owns the Notion
	// account identified by notionUserID.
	AccountInUse(ctx context.Context, key model.UserKey, notionUserID model.NotionUserID) (bool, error)
	// UpdateIfUnchanged replaces the credential with next only while it still
	// holds the token ciphertext of expected, moving the ownership when next
	// is for another Notion account. It reports whether it replaced the
	// credential; a missing or replaced credential is not an error. It fails
	// with ErrNotionAccountInUse when another user owns next's Notion account.
	UpdateIfUnchanged(ctx context.Context, key model.UserKey, expected, next *model.NotionCredential) (bool, error)
	// DeleteIfUnchanged removes the credential, and the ownership of its
	// Notion account, only while the credential still holds the ciphertext of
	// expected. It reports whether a credential was deleted; a missing or
	// replaced credential is not an error.
	DeleteIfUnchanged(ctx context.Context, key model.UserKey, expected *model.NotionCredential) (bool, error)
}

// GitHubCredentialRepository keeps at most one connection per user and
// connects each GitHub account to at most one user. The owner of a GitHub
// account is recorded outside the user's document; it is written and deleted
// together with the connection and never returned.
type GitHubCredentialRepository interface {
	// Create stores cred and makes key the owner of its GitHub account,
	// atomically. It fails with ErrAlreadyExists when key already has a
	// connection, and with ErrGitHubAccountInUse when another user owns the
	// GitHub account.
	Create(ctx context.Context, key model.UserKey, cred *model.GitHubCredential) error
	Get(ctx context.Context, key model.UserKey) (*model.GitHubCredential, error)
	// AccountInUse reports whether a user other than key owns the GitHub
	// account.
	AccountInUse(ctx context.Context, key model.UserKey, id model.GitHubUserID) (bool, error)
	// AcquireRefreshLease sets the refresh lease to leaseID until expiresAt
	// when no lease is held at now. It returns the connection as stored after
	// the call and whether leaseID holds the lease. It fails with ErrNotFound
	// when key has no connection.
	AcquireRefreshLease(ctx context.Context, key model.UserKey, leaseID string, now, expiresAt time.Time) (*model.GitHubCredential, bool, error)
	// ReplaceIfLeaseHeld writes cred only while the stored connection has the
	// same ConnectionID and its lease is leaseID. cred must carry no lease.
	ReplaceIfLeaseHeld(ctx context.Context, key model.UserKey, leaseID string, cred *model.GitHubCredential) (bool, error)
	// ReleaseRefreshLease clears the lease when leaseID holds it and does
	// nothing otherwise. A missing connection is not an error.
	ReleaseRefreshLease(ctx context.Context, key model.UserKey, leaseID string) error
	// DeleteIfConnection deletes the connection, and the ownership of its
	// GitHub account, only while its ConnectionID is connectionID. It reports
	// whether a connection was deleted; a missing or replaced connection is
	// not an error.
	DeleteIfConnection(ctx context.Context, key model.UserKey, connectionID string) (bool, error)
}

type SessionRepository interface {
	// Create fails with ErrAlreadyExists when the ID is taken.
	Create(ctx context.Context, session *auth.Session) error
	Get(ctx context.Context, id auth.SessionID) (*auth.Session, error)
	// Delete removes the session. A missing session is not an error.
	Delete(ctx context.Context, id auth.SessionID) error
}

type SlackEventRepository interface {
	// Claim creates the claim if absent. It returns true only for the first
	// caller, across all instances.
	Claim(ctx context.Context, claim *model.SlackEventClaim) (bool, error)
}
