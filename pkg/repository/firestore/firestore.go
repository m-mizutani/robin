package firestore

import (
	"context"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

const (
	teamsCollection                = "teams"
	usersCollection                = "users"
	credentialsCollection          = "credentials"
	slackCredentialDocID           = "slack"
	googleWorkspaceCredentialDocID = "google_workspace"
	// googleWorkspaceAccountsCollection is keyed by the Google account and
	// names the only user it is connected to.
	googleWorkspaceAccountsCollection = "googleWorkspaceAccounts"
	notionCredentialDocID             = "notion"
	// notionAccountsCollection is keyed by the Notion user and names the only
	// user the Notion account is connected to.
	notionAccountsCollection = "notionAccounts"
	githubCredentialDocID    = "github"
	// githubAccountsCollection is keyed by the GitHub user ID and names the
	// only user the GitHub account is connected to.
	githubAccountsCollection = "githubAccounts"
	sessionsCollection       = "sessions"
	slackEventsCollection    = "slackEvents"
	agentSessionsCollection  = "agentSessions"
	// agentSessionMessagesCollection is a subcollection of one agent session.
	agentSessionMessagesCollection = "agentSessionMessages"
	// agentThreadsCollection is keyed by the agent session ID and names the
	// only user who owns the conversation of the thread.
	agentThreadsCollection = "agentThreads"
	jobsCollection         = "jobs"
	// jobRunsCollection is a subcollection of one job.
	jobRunsCollection = "jobRuns"
	// schedulesCollection is keyed by the job ID and holds the owner and the
	// next run time of every job, so the scheduler can find due jobs.
	schedulesCollection = "schedules"
)

type Firestore struct {
	client                    *firestore.Client
	user                      *userRepository
	slackCredential           *slackCredentialRepository
	googleWorkspaceCredential *googleWorkspaceCredentialRepository
	notionCredential          *notionCredentialRepository
	githubCredential          *githubCredentialRepository
	session                   *sessionRepository
	slackEvent                *slackEventRepository
	agentSession              *agentSessionRepository
	job                       *jobRepository
}

var _ interfaces.Repository = &Firestore{}

// New connects to Firestore. An empty databaseID selects the default database.
func New(ctx context.Context, projectID, databaseID string) (*Firestore, error) {
	var client *firestore.Client
	var err error
	if databaseID != "" {
		client, err = firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	} else {
		client, err = firestore.NewClient(ctx, projectID)
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create firestore client",
			goerr.V("project_id", projectID),
			goerr.V("database_id", databaseID),
		)
	}

	return &Firestore{
		client:                    client,
		user:                      &userRepository{client: client},
		slackCredential:           &slackCredentialRepository{client: client},
		googleWorkspaceCredential: &googleWorkspaceCredentialRepository{client: client},
		notionCredential:          &notionCredentialRepository{client: client},
		githubCredential:          &githubCredentialRepository{client: client},
		session:                   &sessionRepository{client: client},
		slackEvent:                &slackEventRepository{client: client},
		agentSession:              &agentSessionRepository{client: client},
		job:                       &jobRepository{client: client},
	}, nil
}

func (f *Firestore) Job() interfaces.JobRepository { return f.job }

func (f *Firestore) User() interfaces.UserRepository                       { return f.user }
func (f *Firestore) SlackCredential() interfaces.SlackCredentialRepository { return f.slackCredential }
func (f *Firestore) GoogleWorkspaceCredential() interfaces.GoogleWorkspaceCredentialRepository {
	return f.googleWorkspaceCredential
}
func (f *Firestore) NotionCredential() interfaces.NotionCredentialRepository {
	return f.notionCredential
}
func (f *Firestore) GitHubCredential() interfaces.GitHubCredentialRepository {
	return f.githubCredential
}
func (f *Firestore) Session() interfaces.SessionRepository       { return f.session }
func (f *Firestore) SlackEvent() interfaces.SlackEventRepository { return f.slackEvent }
func (f *Firestore) AgentSession() interfaces.AgentSessionRepository {
	return f.agentSession
}

func (f *Firestore) Close() error {
	return f.client.Close()
}

// userDoc is the only place that builds a user document path. All user-owned
// documents hang below it, so a path for another user can only be produced
// from another user's key.
func userDoc(client *firestore.Client, key model.UserKey) *firestore.DocumentRef {
	return client.Collection(teamsCollection).Doc(string(key.TeamID)).
		Collection(usersCollection).Doc(string(key.UserID))
}
