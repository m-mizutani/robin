package interfaces

import (
	"context"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

// LLMClient starts conversations with one model of one provider. Each
// provider (Claude now; Gemini or OpenAI later) has its own adapter, and the
// history format is the adapter's own. NewSession fails with
// ErrLLMHistoryIncompatible when history was written in a format this client
// does not read (another provider, or an unknown version). How SystemNotice
// is delivered (a mid-conversation system message for Claude) is up to each
// adapter.
type LLMClient interface {
	// NewSession continues the conversation recorded in history, or starts a
	// new one when history is empty. The system prompt and tools must be the
	// same for every call on one conversation.
	NewSession(cfg model.LLMSessionConfig, history []model.LLMHistoryMessage) (LLMSession, error)
}

// LLMSession is one conversation. The history is append-only: a failed
// Send leaves it as it was.
type LLMSession interface {
	Send(ctx context.Context, in model.LLMInput) (*model.LLMTurn, error)
	// Appended returns the messages added to the history since NewSession, in order.
	Appended() []model.LLMHistoryMessage
}
