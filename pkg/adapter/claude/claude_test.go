package claude_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/claude"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type recorded struct {
	header http.Header
	body   map[string]any
	raw    []byte
}

// fakeClaude answers each request with the next response and records it.
type fakeClaude struct {
	mu        sync.Mutex
	server    *httptest.Server
	responses []fakeResponse
	requests  []recorded
}

type fakeResponse struct {
	status int
	body   string
}

func newFakeClaude(t *testing.T, responses ...fakeResponse) *fakeClaude {
	t.Helper()
	f := &fakeClaude{responses: responses}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{header: r.Header.Clone(), body: body, raw: raw})
		var resp fakeResponse
		if len(f.responses) > 0 {
			resp, f.responses = f.responses[0], f.responses[1:]
		} else {
			resp = fakeResponse{status: http.StatusInternalServerError, body: `{"type":"error","error":{"type":"api_error","message":"no response"}}`}
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeClaude) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func (f *fakeClaude) client() *claude.Client {
	return claude.New(claude.Config{Model: "claude-sonnet-5-5", MaxTokens: 16000, Effort: anthropic.BetaOutputConfigEffortMedium},
		option.WithBaseURL(f.server.URL), option.WithAPIKey("test-key"), option.WithMaxRetries(0))
}

var sessionConfig = model.LLMSessionConfig{
	SystemPrompt: "You are Robin.",
	Tools: []model.LLMToolSpec{{
		Name:        "notion_search",
		Description: "Search Notion.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
	}},
}

const toolUseResponse = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5",
"content":[
 {"type":"compaction","content":"summary of earlier turns","encrypted_content":"enc","signature":"csig"},
 {"type":"thinking","thinking":"I should look in Notion.","signature":"sig1"},
 {"type":"thinking","thinking":"","signature":"sig2"},
 {"type":"redacted_thinking","data":"opaque"},
 {"type":"text","text":"Let me search Notion."},
 {"type":"tool_use","id":"toolu_1","name":"notion_search","input":{"query":"plan"}}
],
"stop_reason":"tool_use","stop_sequence":null,
"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5,
 "iterations":[{"type":"compaction","input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":3,"cache_creation_input_tokens":4},{"type":"message","input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}]}}`

const endTurnResponse = `{"id":"msg_2","type":"message","role":"assistant","model":"claude-sonnet-5-5",
"content":[{"type":"text","text":"Here is the plan."}],
"stop_reason":"end_turn","stop_sequence":null,
"usage":{"input_tokens":20,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`

func TestSession_Request(t *testing.T) {
	f := newFakeClaude(t, fakeResponse{status: 200, body: toolUseResponse}, fakeResponse{status: 200, body: endTurnResponse})
	s, err := f.client().NewSession(sessionConfig, nil)
	gt.NoError(t, err).Required()

	turn, err := s.Send(context.Background(), model.LLMInput{UserText: "find the plan", SystemNotice: "Budget: $0.00 of $2.00 used, 0 of 20 model calls."})
	gt.NoError(t, err).Required()

	req := f.recorded()[0]
	gt.String(t, req.header.Get("X-Api-Key")).Equal("test-key")
	beta := strings.Join(req.header.Values("Anthropic-Beta"), ",")
	for _, want := range []string{"thinking-display-updates-2026-08-18", "compact-2026-01-12", "thinking-binding-controls-2026-08-01"} {
		gt.Bool(t, strings.Contains(beta, want)).True()
	}

	b := req.body
	gt.Value(t, b["model"]).Equal("claude-sonnet-5-5")
	gt.Value(t, b["max_tokens"]).Equal(float64(16000))
	gt.Value(t, b["thinking"]).Equal(map[string]any{"type": "adaptive", "display": "updates", "block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"}})
	gt.Value(t, b["output_config"]).Equal(map[string]any{"effort": "medium"})
	gt.Value(t, b["context_management"]).Equal(map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}})
	gt.Value(t, b["cache_control"]).Equal(map[string]any{"type": "ephemeral"})
	gt.Value(t, b["system"]).Equal([]any{map[string]any{"type": "text", "text": "You are Robin."}})
	gt.Value(t, b["tool_choice"]).Nil()
	gt.Value(t, b["tools"]).Equal([]any{map[string]any{
		"name":        "notion_search",
		"description": "Search Notion.",
		"input_schema": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"query": map[string]any{"type": "string"}},
			"required":             []any{"query"},
			"additionalProperties": false,
		},
	}})
	gt.Value(t, b["messages"]).Equal([]any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "find the plan"}}},
		map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "Budget: $0.00 of $2.00 used, 0 of 20 model calls."}}},
	})

	// The response becomes progress, text and a tool call; compaction and
	// empty or redacted thinking are not shown.
	gt.Equal(t, turn.Blocks, []model.LLMOutputBlock{
		{Kind: model.LLMOutputProgress, Text: "I should look in Notion."},
		{Kind: model.LLMOutputText, Text: "Let me search Notion."},
		{Kind: model.LLMOutputToolCall, ToolCall: &model.LLMToolCall{ID: "toolu_1", Name: "notion_search", Input: json.RawMessage(`{"query":"plan"}`)}},
	})
	gt.Value(t, turn.StopReason).Equal(model.LLMStopToolUse)
	gt.Equal(t, turn.Usage, model.LLMUsage{InputTokens: 1100, OutputTokens: 250, CacheReadInputTokens: 13, CacheCreationInputTokens: 9})

	_, err = s.Send(context.Background(), model.LLMInput{
		ToolResults:  []model.LLMToolResult{{CallID: "toolu_1", Content: `{"results":[]}`}, {CallID: "toolu_2", Content: "Not found.", IsError: true}},
		SystemNotice: "Budget: $0.01 of $2.00 used, 1 of 20 model calls.",
		DisableTools: true,
	})
	gt.NoError(t, err).Required()
	second := f.recorded()[1].body
	gt.Value(t, second["tool_choice"]).Equal(map[string]any{"type": "none"})
	msgs := second["messages"].([]any)
	gt.Array(t, msgs).Length(5).Required()
	gt.Value(t, msgs[3]).Equal(map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": []any{map[string]any{"type": "text", "text": `{"results":[]}`}}, "is_error": false},
		map[string]any{"type": "tool_result", "tool_use_id": "toolu_2", "content": []any{map[string]any{"type": "text", "text": "Not found."}}, "is_error": true},
	}})
	gt.Value(t, msgs[4].(map[string]any)["role"]).Equal("system")
}

func TestSession_RequestWithoutTools(t *testing.T) {
	f := newFakeClaude(t, fakeResponse{status: 200, body: endTurnResponse})
	s, err := f.client().NewSession(model.LLMSessionConfig{SystemPrompt: "You are Robin."}, nil)
	gt.NoError(t, err).Required()
	_, err = s.Send(context.Background(), model.LLMInput{UserText: "say hello"})
	gt.NoError(t, err).Required()

	body := f.recorded()[0].body
	_, hasTools := body["tools"]
	gt.False(t, hasTools)
	gt.Value(t, body["tool_choice"]).Nil()
}

func TestSession_StopReasons(t *testing.T) {
	cases := map[string]model.LLMStopReason{
		"end_turn":      model.LLMStopEndTurn,
		"tool_use":      model.LLMStopToolUse,
		"max_tokens":    model.LLMStopMaxTokens,
		"refusal":       model.LLMStopRefusal,
		"pause_turn":    model.LLMStopOther,
		"stop_sequence": model.LLMStopOther,
	}
	for reason, want := range cases {
		t.Run(reason, func(t *testing.T) {
			body := strings.Replace(endTurnResponse, `"stop_reason":"end_turn"`, `"stop_reason":"`+reason+`"`, 1)
			f := newFakeClaude(t, fakeResponse{status: 200, body: body})
			s, err := f.client().NewSession(sessionConfig, nil)
			gt.NoError(t, err).Required()
			turn, err := s.Send(context.Background(), model.LLMInput{UserText: "hi"})
			gt.NoError(t, err).Required()
			gt.Value(t, turn.StopReason).Equal(want)
		})
	}
}

func TestSession_History(t *testing.T) {
	f := newFakeClaude(t,
		fakeResponse{status: 200, body: toolUseResponse},
		fakeResponse{status: http.StatusInternalServerError, body: `{"type":"error","error":{"type":"api_error","message":"boom"}}`},
		fakeResponse{status: 200, body: endTurnResponse},
	)
	s, err := f.client().NewSession(sessionConfig, nil)
	gt.NoError(t, err).Required()
	_, err = s.Send(context.Background(), model.LLMInput{UserText: "first", SystemNotice: "Budget: x"})
	gt.NoError(t, err).Required()

	// A failed call leaves the history as it was.
	_, err = s.Send(context.Background(), model.LLMInput{UserText: "lost"})
	gt.Error(t, err)

	appended := s.Appended()
	gt.Array(t, appended).Length(3).Required()
	for _, m := range appended {
		gt.String(t, m.Format).Equal("anthropic.beta-message-param.v1")
	}

	restored, err := f.client().NewSession(sessionConfig, appended)
	gt.NoError(t, err).Required()
	_, err = restored.Send(context.Background(), model.LLMInput{UserText: "second"})
	gt.NoError(t, err).Required()

	msgs := f.recorded()[2].body["messages"].([]any)
	gt.Array(t, msgs).Length(4).Required()
	gt.Value(t, msgs[3]).Equal(map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "second"}}})

	// The assistant message is sent back with every block as the API returned
	// it: compaction, thinking signatures and the tool call.
	var sent struct {
		Messages []json.RawMessage `json:"messages"`
	}
	gt.NoError(t, json.Unmarshal(f.recorded()[2].raw, &sent)).Required()
	var assistant map[string]any
	gt.NoError(t, json.Unmarshal(sent.Messages[2], &assistant)).Required()
	gt.Value(t, assistant["role"]).Equal("assistant")
	content := assistant["content"].([]any)
	gt.Array(t, content).Length(6).Required()
	gt.Value(t, content[0]).Equal(map[string]any{"type": "compaction", "content": "summary of earlier turns", "encrypted_content": "enc", "signature": "csig"})
	gt.Value(t, content[1]).Equal(map[string]any{"type": "thinking", "thinking": "I should look in Notion.", "signature": "sig1"})
	gt.Value(t, content[3]).Equal(map[string]any{"type": "redacted_thinking", "data": "opaque"})
	gt.Value(t, content[5]).Equal(map[string]any{"type": "tool_use", "id": "toolu_1", "name": "notion_search", "input": map[string]any{"query": "plan"}})
	gt.String(t, string(sent.Messages[2])).Equal(string(appended[2].Data))

	for _, raw := range sent.Messages {
		gt.Bool(t, !strings.Contains(string(raw), "lost")).True()
	}
}

func TestNewSession_IncompatibleHistory(t *testing.T) {
	f := newFakeClaude(t)
	_, err := f.client().NewSession(sessionConfig, []model.LLMHistoryMessage{{Format: "openai.v1", Data: []byte("{}")}})
	gt.Error(t, err).Is(interfaces.ErrLLMHistoryIncompatible)
}

func TestSession_ServerError(t *testing.T) {
	f := newFakeClaude(t, fakeResponse{status: http.StatusInternalServerError, body: `{"type":"error","error":{"type":"api_error","message":"boom"}}`})
	s, err := f.client().NewSession(sessionConfig, nil)
	gt.NoError(t, err).Required()
	_, err = s.Send(context.Background(), model.LLMInput{UserText: "hi"})
	gt.Error(t, err)
	gt.Array(t, s.Appended()).Length(0)
}
