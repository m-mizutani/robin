package model

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

var llmToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// LLMToolSpec is a tool as the model sees it.
type LLMToolSpec struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema of type object.
	InputSchema json.RawMessage
}

func (x LLMToolSpec) Validate() error {
	if !llmToolNamePattern.MatchString(x.Name) {
		return goerr.New("invalid llm tool name", goerr.V("name", x.Name))
	}
	if x.Description == "" {
		return goerr.New("empty llm tool description", goerr.V("name", x.Name))
	}
	var schema struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(x.InputSchema, &schema); err != nil {
		return goerr.Wrap(err, "llm tool input schema is not JSON", goerr.V("name", x.Name))
	}
	if schema.Type != "object" {
		return goerr.New("llm tool input schema is not an object", goerr.V("name", x.Name))
	}
	return nil
}

// LLMSessionConfig is the fixed part of a conversation. The same value has to
// be given for every call on one conversation, because the model treats its
// earlier reasoning as invalid when the system prompt or the tools change.
type LLMSessionConfig struct {
	SystemPrompt string
	Tools        []LLMToolSpec
}

// LLMHistoryMessage is one message of a conversation in the format of the
// adapter that wrote it. Callers store and return it without reading Data.
type LLMHistoryMessage struct {
	Format string
	Data   []byte
}

// LLMInput is what one call adds to the conversation.
type LLMInput struct {
	UserText    string
	ToolResults []LLMToolResult
	// SystemNotice follows the user content as a system message written by
	// Robin. Empty adds no message.
	SystemNotice string
	// DisableTools keeps the tools defined but lets the model call none.
	DisableTools bool
}

func (x LLMInput) Validate() error {
	if x.UserText == "" && len(x.ToolResults) == 0 {
		return goerr.New("llm input has neither text nor tool results")
	}
	for _, r := range x.ToolResults {
		if r.CallID == "" {
			return goerr.New("llm tool result has no call ID")
		}
	}
	return nil
}

// LLMToolResult answers the tool call whose ID is CallID.
type LLMToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// LLMToolCall is a tool call requested by the model.
type LLMToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// LLMOutputKind is the kind of one block of a model response.
type LLMOutputKind string

const (
	// LLMOutputProgress is a note the model writes between steps (a
	// thinking summary). It is shown as progress, never as the answer.
	LLMOutputProgress LLMOutputKind = "progress"
	LLMOutputText     LLMOutputKind = "text"
	LLMOutputToolCall LLMOutputKind = "tool_call"
)

// LLMOutputBlock is one block of a model response. ToolCall is set only for
// LLMOutputToolCall.
type LLMOutputBlock struct {
	Kind     LLMOutputKind
	Text     string
	ToolCall *LLMToolCall
}

// LLMStopReason is why the model stopped.
type LLMStopReason string

const (
	LLMStopEndTurn   LLMStopReason = "end_turn"
	LLMStopToolUse   LLMStopReason = "tool_use"
	LLMStopMaxTokens LLMStopReason = "max_tokens"
	LLMStopRefusal   LLMStopReason = "refusal"
	LLMStopOther     LLMStopReason = "other"
)

// LLMUsage counts the tokens of one call. InputTokens excludes the tokens
// read from or written to the prompt cache.
type LLMUsage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
}

func (u LLMUsage) Add(v LLMUsage) LLMUsage {
	return LLMUsage{
		InputTokens:              u.InputTokens + v.InputTokens,
		OutputTokens:             u.OutputTokens + v.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens + v.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens + v.CacheCreationInputTokens,
	}
}

// LLMTurn is one model response.
type LLMTurn struct {
	Blocks     []LLMOutputBlock
	StopReason LLMStopReason
	Usage      LLMUsage
}

func (t *LLMTurn) ToolCalls() []LLMToolCall {
	var calls []LLMToolCall
	for _, b := range t.Blocks {
		if b.Kind == LLMOutputToolCall && b.ToolCall != nil {
			calls = append(calls, *b.ToolCall)
		}
	}
	return calls
}

// Text joins the text blocks with a newline.
func (t *LLMTurn) Text() string {
	var texts []string
	for _, b := range t.Blocks {
		if b.Kind == LLMOutputText && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}
