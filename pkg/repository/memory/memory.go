// Package memory is an in-process repository for development and usecase
// tests. It holds data only for the lifetime of one process, so it must not be
// used by a deployment that runs more than one instance.
package memory

import (
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
)

type Memory struct {
	user                      *userRepository
	slackCredential           *slackCredentialRepository
	googleWorkspaceCredential *googleWorkspaceCredentialRepository
	notionCredential          *notionCredentialRepository
	githubCredential          *githubCredentialRepository
	session                   *sessionRepository
	slackEvent                *slackEventRepository
	agentSession              *agentSessionRepository
}

var _ interfaces.Repository = &Memory{}

func New() *Memory {
	return &Memory{
		user:                      newUserRepository(),
		slackCredential:           newSlackCredentialRepository(),
		googleWorkspaceCredential: newGoogleWorkspaceCredentialRepository(),
		notionCredential:          newNotionCredentialRepository(),
		githubCredential:          newGitHubCredentialRepository(),
		session:                   newSessionRepository(),
		slackEvent:                newSlackEventRepository(),
		agentSession:              newAgentSessionRepository(),
	}
}

func (m *Memory) User() interfaces.UserRepository                       { return m.user }
func (m *Memory) SlackCredential() interfaces.SlackCredentialRepository { return m.slackCredential }
func (m *Memory) GoogleWorkspaceCredential() interfaces.GoogleWorkspaceCredentialRepository {
	return m.googleWorkspaceCredential
}
func (m *Memory) NotionCredential() interfaces.NotionCredentialRepository {
	return m.notionCredential
}
func (m *Memory) GitHubCredential() interfaces.GitHubCredentialRepository {
	return m.githubCredential
}
func (m *Memory) Session() interfaces.SessionRepository       { return m.session }
func (m *Memory) SlackEvent() interfaces.SlackEventRepository { return m.slackEvent }
func (m *Memory) AgentSession() interfaces.AgentSessionRepository {
	return m.agentSession
}

func (m *Memory) Close() error {
	return nil
}
