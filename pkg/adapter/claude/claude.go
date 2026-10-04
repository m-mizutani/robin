// Package claude implements the LLM boundary with the Messages API of the
// Anthropic Go SDK, through the Claude API or Vertex AI.
package claude

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// historyFormat is the Format of every message this adapter writes: the JSON
// of one anthropic.BetaMessageParam. Change the version when the encoding
// changes, and keep reading the old one.
const historyFormat = "anthropic.beta-message-param.v1"

// betas are the beta features every request uses: thinking summaries between
// steps (display "updates"), server-side compaction, and dropping a thinking
// block that fails the conversation check instead of failing the request.
var betas = []anthropic.AnthropicBeta{
	anthropic.AnthropicBetaThinkingDisplayUpdates2026_08_18,
	anthropic.AnthropicBetaCompact2026_01_12,
	anthropic.AnthropicBetaThinkingBindingControls2026_08_01,
}

type Config struct {
	Model     string
	MaxTokens int64
	Effort    anthropic.BetaOutputConfigEffort
}

func (c Config) Validate() error {
	if c.Model == "" {
		return goerr.New("empty claude model")
	}
	if c.MaxTokens <= 0 {
		return goerr.New("claude max tokens must be positive", goerr.V("max_tokens", c.MaxTokens))
	}
	if c.Effort == "" {
		return goerr.New("empty claude effort")
	}
	return nil
}

type Client struct {
	client anthropic.Client
	cfg    Config
}

var _ interfaces.LLMClient = &Client{}

// New takes the SDK request options that select the endpoint and the
// credential (vertex.WithGoogleAuth, or option.WithAPIKey).
func New(cfg Config, opts ...option.RequestOption) *Client {
	return &Client{client: anthropic.NewClient(opts...), cfg: cfg}
}

func toolParams(specs []model.LLMToolSpec) ([]anthropic.BetaToolUnionParam, error) {
	tools := make([]anthropic.BetaToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		var schema map[string]any
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			return nil, goerr.Wrap(err, "invalid tool input schema", goerr.V("tool", spec.Name))
		}
		input := anthropic.BetaToolInputSchemaParam{Properties: schema["properties"]}
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				if s, ok := r.(string); ok {
					input.Required = append(input.Required, s)
				}
			}
		}
		delete(schema, "type")
		delete(schema, "properties")
		delete(schema, "required")
		if len(schema) > 0 {
			input.ExtraFields = schema
		}
		tool := anthropic.BetaToolUnionParamOfTool(input, spec.Name)
		tool.OfTool.Description = anthropic.String(spec.Description)
		tools = append(tools, tool)
	}
	return tools, nil
}

func (c *Client) NewSession(cfg model.LLMSessionConfig, history []model.LLMHistoryMessage) (interfaces.LLMSession, error) {
	if err := c.cfg.Validate(); err != nil {
		return nil, err
	}
	tools, err := toolParams(cfg.Tools)
	if err != nil {
		return nil, err
	}
	msgs := make([]anthropic.BetaMessageParam, 0, len(history))
	for i, h := range history {
		if h.Format != historyFormat {
			return nil, goerr.Wrap(interfaces.ErrLLMHistoryIncompatible, "history message has another format",
				goerr.V("format", h.Format), goerr.V("index", i))
		}
		var m anthropic.BetaMessageParam
		if err := json.Unmarshal(h.Data, &m); err != nil {
			return nil, goerr.Wrap(err, "failed to decode history message", goerr.V("index", i))
		}
		msgs = append(msgs, m)
	}
	return &session{client: c, system: cfg.SystemPrompt, tools: tools, history: msgs}, nil
}

type session struct {
	client   *Client
	system   string
	tools    []anthropic.BetaToolUnionParam
	history  []anthropic.BetaMessageParam
	appended []model.LLMHistoryMessage
}

func inputMessages(in model.LLMInput) []anthropic.BetaMessageParam {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(in.ToolResults)+1)
	for _, r := range in.ToolResults {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.CallID, r.Content, r.IsError))
	}
	if in.UserText != "" {
		blocks = append(blocks, anthropic.NewBetaTextBlock(in.UserText))
	}
	msgs := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(blocks...)}
	if in.SystemNotice != "" {
		msgs = append(msgs, anthropic.NewBetaSystemMessage(anthropic.BetaSystemMessageOutputConfigParam{},
			anthropic.NewBetaTextBlock(in.SystemNotice)))
	}
	return msgs
}

func (s *session) params(added []anthropic.BetaMessageParam, disableTools bool) anthropic.BetaMessageNewParams {
	cfg := s.client.cfg
	messages := make([]anthropic.BetaMessageParam, 0, len(s.history)+len(added))
	messages = append(append(messages, s.history...), added...)
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: cfg.MaxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: s.system}},
		Tools:     s.tools,
		Messages:  messages,
		Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplayUpdates,
			BlockBinding: anthropic.BetaThinkingBlockBindingParam{
				PrefixMismatchBehavior: anthropic.BetaThinkingPrefixMismatchBehaviorDropBlock,
			},
		}},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: cfg.Effort},
		ContextManagement: anthropic.BetaContextManagementConfigParam{
			Edits: []anthropic.BetaContextManagementConfigEditUnionParam{
				{OfCompact20260112: &anthropic.BetaCompact20260112EditParam{}},
			},
		},
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		Betas:        betas,
	}
	if disableTools {
		none := anthropic.NewBetaToolChoiceNoneParam()
		p.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfNone: &none}
	}
	return p
}

func (s *session) Send(ctx context.Context, in model.LLMInput) (*model.LLMTurn, error) {
	if err := in.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid llm input")
	}
	added := inputMessages(in)
	resp, err := s.client.client.Beta.Messages.New(ctx, s.params(added, in.DisableTools))
	if err != nil {
		return nil, goerr.Wrap(err, "claude messages request failed", goerr.V("model", s.client.cfg.Model))
	}

	added = append(added, resp.ToParam())
	encoded := make([]model.LLMHistoryMessage, 0, len(added))
	for _, m := range added {
		data, err := json.Marshal(m)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to encode history message")
		}
		encoded = append(encoded, model.LLMHistoryMessage{Format: historyFormat, Data: data})
	}
	s.history = append(s.history, added...)
	s.appended = append(s.appended, encoded...)
	return toTurn(resp), nil
}

func toTurn(resp *anthropic.BetaMessage) *model.LLMTurn {
	turn := &model.LLMTurn{StopReason: stopReason(resp.StopReason), Usage: usage(resp.Usage)}
	for _, c := range resp.Content {
		switch c.Type {
		case "thinking":
			if c.Thinking != "" {
				turn.Blocks = append(turn.Blocks, model.LLMOutputBlock{Kind: model.LLMOutputProgress, Text: c.Thinking})
			}
		case "text":
			turn.Blocks = append(turn.Blocks, model.LLMOutputBlock{Kind: model.LLMOutputText, Text: c.Text})
		case "tool_use":
			turn.Blocks = append(turn.Blocks, model.LLMOutputBlock{
				Kind:     model.LLMOutputToolCall,
				ToolCall: &model.LLMToolCall{ID: c.ID, Name: c.Name, Input: c.Input},
			})
		}
	}
	return turn
}

func stopReason(r anthropic.BetaStopReason) model.LLMStopReason {
	switch r {
	case anthropic.BetaStopReasonEndTurn:
		return model.LLMStopEndTurn
	case anthropic.BetaStopReasonToolUse:
		return model.LLMStopToolUse
	case anthropic.BetaStopReasonMaxTokens:
		return model.LLMStopMaxTokens
	case anthropic.BetaStopReasonRefusal:
		return model.LLMStopRefusal
	default:
		return model.LLMStopOther
	}
}

// usage adds the compaction iterations, whose tokens are not included in the
// top-level usage.
func usage(u anthropic.BetaUsage) model.LLMUsage {
	out := model.LLMUsage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
	for _, it := range u.Iterations {
		if it.Type != "compaction" {
			continue
		}
		out = out.Add(model.LLMUsage{
			InputTokens:              it.InputTokens,
			OutputTokens:             it.OutputTokens,
			CacheReadInputTokens:     it.CacheReadInputTokens,
			CacheCreationInputTokens: it.CacheCreationInputTokens,
		})
	}
	return out
}

func (s *session) Appended() []model.LLMHistoryMessage {
	return append([]model.LLMHistoryMessage(nil), s.appended...)
}
