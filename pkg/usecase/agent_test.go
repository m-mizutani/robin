package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
)

// fakeStep is one scripted answer of the fake model.
type fakeStep struct {
	turn *model.LLMTurn
	err  error
	// wait blocks until the context ends and returns its error.
	wait bool
}

// fakeLLM answers Send with the scripted steps in order and records every
// session it started and every input. Each successful Send appends two
// history messages, "in-N" and "out-N".
type fakeLLM struct {
	mu            sync.Mutex
	steps         []fakeStep
	newSessionErr error
	configs       []model.LLMSessionConfig
	histories     [][]model.LLMHistoryMessage
	inputs        []model.LLMInput
	sent          int
}

func (f *fakeLLM) script(steps ...fakeStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, steps...)
}

func (f *fakeLLM) NewSession(cfg model.LLMSessionConfig, history []model.LLMHistoryMessage) (interfaces.LLMSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs = append(f.configs, cfg)
	f.histories = append(f.histories, append([]model.LLMHistoryMessage(nil), history...))
	if f.newSessionErr != nil {
		return nil, f.newSessionErr
	}
	return &fakeLLMSession{llm: f}, nil
}

func (f *fakeLLM) recordedInputs() []model.LLMInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.LLMInput(nil), f.inputs...)
}

type fakeLLMSession struct {
	llm      *fakeLLM
	appended []model.LLMHistoryMessage
}

func (s *fakeLLMSession) Send(ctx context.Context, in model.LLMInput) (*model.LLMTurn, error) {
	f := s.llm
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	if len(f.steps) == 0 {
		f.mu.Unlock()
		return nil, errors.New("no scripted step")
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	f.sent++
	n := f.sent
	f.mu.Unlock()

	if step.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if step.err != nil {
		return nil, step.err
	}
	s.appended = append(s.appended,
		model.LLMHistoryMessage{Format: "fake.v1", Data: []byte(fmt.Sprintf("in-%d", n))},
		model.LLMHistoryMessage{Format: "fake.v1", Data: []byte(fmt.Sprintf("out-%d", n))})
	return step.turn, nil
}

func (s *fakeLLMSession) Appended() []model.LLMHistoryMessage {
	return append([]model.LLMHistoryMessage(nil), s.appended...)
}

func textTurn(text string, usage model.LLMUsage) fakeStep {
	return fakeStep{turn: &model.LLMTurn{
		Blocks:     []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: text}},
		StopReason: model.LLMStopEndTurn,
		Usage:      usage,
	}}
}

func toolCall(id, name, input string) model.LLMOutputBlock {
	return model.LLMOutputBlock{Kind: model.LLMOutputToolCall, ToolCall: &model.LLMToolCall{ID: id, Name: name, Input: json.RawMessage(input)}}
}

func toolTurn(usage model.LLMUsage, blocks ...model.LLMOutputBlock) fakeStep {
	return fakeStep{turn: &model.LLMTurn{Blocks: blocks, StopReason: model.LLMStopToolUse, Usage: usage}}
}

// usd returns a usage that costs about the given dollars at the test rate
// ($2 per million input tokens).
func usd(dollars float64) model.LLMUsage {
	return model.LLMUsage{InputTokens: int64(dollars * 500_000)}
}

const (
	agentThreadTS   = "1700000000.000100"
	agentMentionTS  = "1700000100.000100"
	agentMentionTS2 = "1700000200.000100"
	agentProgressTS = "1800000000.000001"
)

type agentFixture struct {
	repo          interfaces.Repository
	cipher        *fakeCipher
	bot           *fakeBot
	slackFactory  *fakeUserClientFactory
	slack         *usecase.SlackUserAccess
	notionClients *fakeNotionClients
	notion        *usecase.NotionAccess
	googleClients *fakeGoogleClients
	google        *usecase.GoogleWorkspaceAccess
	llm           *fakeLLM
	services      usecase.AgentServices
	cfg           usecase.AgentConfig
	now           time.Time
	ids           int
}

func testAgentConfig() usecase.AgentConfig {
	return usecase.AgentConfig{
		BaseURL: testBaseURL,
		Rate: model.Rate{
			Input:      model.FromUSDPerMTok(2),
			Output:     model.FromUSDPerMTok(10),
			CacheRead:  model.FromUSDPerMTok(0.2),
			CacheWrite: model.FromUSDPerMTok(2.5),
		},
		Budget:           model.FromUSD(2),
		NoticeRatio:      0.9,
		MaxLLMCalls:      20,
		MaxToolCalls:     10,
		Timeout:          10 * time.Minute,
		LeaseMargin:      time.Minute,
		SessionTTL:       720 * time.Hour,
		ToolResultLimit:  20000,
		ThreadLimit:      50,
		ThreadCharLimit:  20000,
		HistoryByteLimit: 8 << 20,
	}
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	f := &agentFixture{
		repo:          memory.New(),
		cipher:        &fakeCipher{},
		bot:           newFakeBot(),
		slackFactory:  newFakeUserClientFactory(),
		notionClients: &fakeNotionClients{errs: map[model.NotionAccessToken]error{}},
		googleClients: &fakeGoogleClients{},
		llm:           &fakeLLM{},
		cfg:           testAgentConfig(),
		now:           time.Date(2026, 10, 4, 1, 7, 2, 0, time.UTC),
	}
	f.slackFactory.identities[testUserToken] = &model.SlackIdentity{TeamID: testKey.TeamID, UserID: testKey.UserID}
	f.slack = usecase.NewSlackUserAccess(f.repo, f.cipher, f.slackFactory)
	f.notion = usecase.NewNotionAccess(f.repo, f.cipher, newFakeNotionOAuth(), f.notionClients)
	f.google = usecase.NewGoogleWorkspaceAccess(f.repo, f.cipher, f.googleClients)
	f.services = usecase.AgentServices{Notion: f.notion, Google: f.google}

	ctx := context.Background()
	gt.NoError(t, f.slack.Store(ctx, testKey, testUserToken, []string{"search:read"}, f.now)).Required()
	gt.NoError(t, f.notion.Store(ctx, testKey, aliceGrant(model.NotionTokens{AccessToken: "notion-access", RefreshToken: "notion-refresh"}))).Required()
	return f
}

func (f *agentFixture) agent(t *testing.T) *usecase.Agent {
	t.Helper()
	a, err := usecase.NewAgent(f.repo, f.llm, f.bot, f.services, f.cfg)
	gt.NoError(t, err).Required()
	a.SetClockForTest(func() time.Time { return f.now }, func() string {
		f.ids++
		return fmt.Sprintf("id-%d", f.ids)
	})
	return a
}

func (f *agentFixture) request(t *testing.T, mentionTS string) usecase.AgentRequest {
	t.Helper()
	client, err := f.slack.Client(context.Background(), testKey)
	gt.NoError(t, err).Required()
	return usecase.AgentRequest{
		Key:        testKey,
		Slack:      client,
		ChannelID:  testChannel,
		ThreadTS:   agentThreadTS,
		MentionTS:  mentionTS,
		InThread:   true,
		Text:       "<@UROBIN> what is the plan?",
		ProgressTS: agentProgressTS,
	}
}

func (f *agentFixture) sessionID(t *testing.T) model.AgentSessionID {
	t.Helper()
	id, err := model.NewAgentSessionID(testKey.TeamID, testChannel, agentThreadTS)
	gt.NoError(t, err).Required()
	return id
}

// storedMessages returns the messages of the session's current generation.
func (f *agentFixture) storedMessages(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	res, err := f.repo.AgentSession().Begin(ctx, testKey, model.AgentSessionBeginRequest{
		ID: f.sessionID(t), ChannelID: testChannel, ThreadTS: agentThreadTS, LeaseID: "check",
		Now: f.now, LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "check-gen",
	})
	gt.NoError(t, err).Required()
	gt.Value(t, res.Status).NotEqual(model.AgentSessionBusy).Required()
	msgs, err := f.repo.AgentSession().ListMessages(ctx, testKey, f.sessionID(t), res.Session.Generation)
	gt.NoError(t, err).Required()
	gt.NoError(t, f.repo.AgentSession().Release(ctx, testKey, f.sessionID(t), "check")).Required()
	var out []string
	for _, m := range msgs {
		out = append(out, string(m.Data))
	}
	return out
}

func (f *agentFixture) lastProgress(t *testing.T) string {
	t.Helper()
	updates := f.bot.texts("UpdateProgress")
	gt.Array(t, updates).Longer(0).Required()
	return updates[len(updates)-1]
}

func TestAgent_FirstMentionWithoutTools(t *testing.T) {
	f := newAgentFixture(t)
	f.bot.thread = []model.SlackThreadMessage{
		{TS: "1700000000.000100", UserID: "U0123ABCD", Text: "the plan for Q4"},
		{TS: "1700000050.000100", BotID: "B0999", Text: "deploy finished"},
	}
	f.llm.script(textTurn("Here is the plan.", model.LLMUsage{InputTokens: 1000, OutputTokens: 100}))

	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	inputs := f.llm.recordedInputs()
	gt.Array(t, inputs).Length(1).Required()
	in := inputs[0].UserText
	gt.String(t, in).Equal("<thread>\n" +
		"[2023-11-14T22:13:20Z] <@U0123ABCD>: the plan for Q4\n" +
		"[2023-11-14T22:14:10Z] bot B0999: deploy finished\n" +
		"</thread>\n" +
		"<connections>\n" +
		"Slack: connected\n" +
		"Notion: connected\n" +
		"Google Workspace: not connected (connect at https://robin.example.com/settings)\n" +
		"</connections>\n" +
		"<request from=\"<@U0123ABCD>\" at=\"2023-11-14T22:15:00Z\">\n" +
		"<@UROBIN> what is the plan?\n" +
		"</request>\n" +
		"Current time: 2026-10-04T01:07:02Z")
	gt.String(t, inputs[0].SystemNotice).Equal("Budget: $0.00 of $2.00 used, 0 of 20 model calls.")
	gt.False(t, inputs[0].DisableTools)

	gt.Array(t, f.bot.answers()).Equal([]botMessage{{ChannelID: testChannel, ThreadTS: agentThreadTS, Text: "Here is the plan."}})
	gt.Equal(t, f.bot.texts("GetThreadMessages"), []string{"after= before=" + agentMentionTS + " limit=50"})
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 1 LLM call · 0 tool calls · $0.00")
	gt.Equal(t, f.storedMessages(t), []string{"in-1", "out-1"})
	for _, c := range f.bot.recorded("PostAnswer", "UpdateProgress") {
		gt.Value(t, c.Requester).Equal(testKey.UserID)
	}
	for _, c := range f.bot.recorded("UpdateProgress") {
		gt.String(t, c.TS).Equal(agentProgressTS)
	}
}

func TestAgent_SecondMentionContinuesTheConversation(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(textTurn("first answer", model.LLMUsage{}), textTurn("second answer", model.LLMUsage{}))
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	f.bot.thread = []model.SlackThreadMessage{
		{TS: "1700000150.000100", BotID: "B1", FromRobin: true, Text: "first answer"},
		{TS: "1700000160.000100", UserID: "U0999", Text: "a comment from someone else"},
	}
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS2))).Required()

	gt.Array(t, f.llm.histories).Length(2).Required()
	gt.Array(t, f.llm.histories[0]).Length(0)
	gt.Equal(t, f.llm.histories[1], []model.LLMHistoryMessage{
		{Format: "fake.v1", Data: []byte("in-1")}, {Format: "fake.v1", Data: []byte("out-1")},
	})
	gt.Equal(t, f.bot.texts("GetThreadMessages")[1], "after="+agentMentionTS+" before="+agentMentionTS2+" limit=50")
	second := f.llm.recordedInputs()[1].UserText
	gt.String(t, second).Contains("<@U0999>: a comment from someone else")
	gt.False(t, strings.Contains(second, "first answer"))
	gt.Equal(t, f.storedMessages(t), []string{"in-1", "out-1", "in-2", "out-2"})
}

func TestAgent_FirstMentionAtTheTopLevelReadsNoThread(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(textTurn("answer", model.LLMUsage{}))
	req := f.request(t, agentThreadTS)
	req.InThread = false
	gt.NoError(t, f.agent(t).Run(context.Background(), req)).Required()
	gt.Array(t, f.bot.recorded("GetThreadMessages")).Length(0)
	gt.False(t, strings.Contains(f.llm.recordedInputs()[0].UserText, "<thread>"))
}

func TestAgent_ToolUse(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(
		toolTurn(model.LLMUsage{InputTokens: 1000},
			model.LLMOutputBlock{Kind: model.LLMOutputProgress, Text: "I should look in Notion."},
			toolCall("call-1", "notion_search", `{"query":"q4 plan"}`)),
		textTurn("The plan is X.", model.LLMUsage{InputTokens: 1000}),
	)

	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	gt.Equal(t, f.bot.texts("UpdateProgress"), []string{
		":thought_balloon: I should look in Notion.",
		":mag: Searching Notion for “q4 plan”",
		":white_check_mark: Done · 2 LLM calls · 1 tool call · $0.00",
	})
	calls := f.notionClients.recorded()
	gt.Array(t, calls).Length(1).Required()
	gt.Value(t, calls[0].Input).Equal(model.NotionSearchQuery{Query: "q4 plan", Page: model.NotionPagination{PageSize: 25}})

	inputs := f.llm.recordedInputs()
	gt.Array(t, inputs).Length(2).Required()
	gt.Array(t, inputs[1].ToolResults).Length(1).Required()
	result := inputs[1].ToolResults[0]
	gt.String(t, result.CallID).Equal("call-1")
	gt.False(t, result.IsError)
	gt.String(t, result.Content).Equal(`{"results":[{"object":"page","id":"p1"}],"next_cursor":"c2","has_more":true}`)
	gt.String(t, inputs[1].UserText).Equal("")
	gt.Equal(t, f.storedMessages(t), []string{"in-1", "out-1", "in-2", "out-2"})
}

func TestAgent_ParallelAndTooManyToolCalls(t *testing.T) {
	f := newAgentFixture(t)
	var blocks []model.LLMOutputBlock
	for i := range 12 {
		blocks = append(blocks, toolCall(fmt.Sprintf("call-%d", i), "slack_search_messages", fmt.Sprintf(`{"query":"q%d"}`, i)))
	}
	f.llm.script(toolTurn(model.LLMUsage{}, blocks...), textTurn("done", model.LLMUsage{}))

	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	gt.Array(t, f.slackFactory.searches).Length(10)
	results := f.llm.recordedInputs()[1].ToolResults
	gt.Array(t, results).Length(12).Required()
	for i, r := range results {
		gt.String(t, r.CallID).Equal(fmt.Sprintf("call-%d", i))
		gt.Equal(t, r.IsError, i >= 10)
	}
	gt.String(t, results[11].Content).Equal("too many tool calls in one step; at most 10 calls run")
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 2 LLM calls · 10 tool calls · $0.00")
}

func TestAgent_ToolsAndPromptAreFixed(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("call-1", "gmail_search", `{"query":"from:bob"}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	var names []string
	for _, spec := range f.llm.configs[0].Tools {
		names = append(names, spec.Name)
	}
	gt.Equal(t, names, []string{
		"slack_search_messages",
		"notion_search", "notion_get_page", "notion_get_block_children", "notion_get_database", "notion_get_data_source", "notion_query_data_source",
		"gmail_search", "gmail_get_message", "drive_search", "drive_get_file", "calendar_list_events",
	})
	gt.String(t, f.llm.recordedInputs()[0].UserText).Contains("Google Workspace: not connected (connect at https://robin.example.com/settings)")
	result := f.llm.recordedInputs()[1].ToolResults[0]
	gt.True(t, result.IsError)
	gt.String(t, result.Content).Equal("Google Workspace is not connected. The user can connect it at https://robin.example.com/settings.")

	// Another user with other connections gets the same prompt and tools.
	other := newAgentFixture(t)
	other.llm.script(textTurn("hi", model.LLMUsage{}))
	gt.NoError(t, other.agent(t).Run(context.Background(), other.request(t, agentMentionTS))).Required()
	gt.Equal(t, other.llm.configs[0], f.llm.configs[0])

	prompt := f.llm.configs[0].SystemPrompt
	gt.String(t, prompt).Contains("Slack messages, Notion and Google Workspace (Gmail, Drive and Calendar)")
	gt.String(t, prompt).Contains("https://robin.example.com/settings")
	gt.String(t, prompt).Contains(`channel_type is "group" (a private channel), "im" (a DM) or "mpim" (a group DM)`)
	gt.String(t, prompt).Contains("Do not quote or copy the text of those messages into your answer")
	gt.String(t, prompt).Contains(`A system message that starts with "Budget:"`)
}

func TestAgent_GitHubTools(t *testing.T) {
	f := newAgentFixture(t)
	users := &fakeGitHubUsers{identity: &model.GitHubIdentity{ID: 1, Login: "alice"}}
	github := usecase.NewGitHubUserAccess(f.repo, f.cipher, &fakeGitHubOAuth{}, users)
	gt.NoError(t, github.Store(context.Background(), testKey, &model.GitHubToken{AccessToken: "ghu_alice"}, &model.GitHubIdentity{ID: 1, Login: "alice"})).Required()
	f.services.GitHub = github
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "github_get_issue", `{"owner":"o","repo":"r","number":7}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	gt.Equal(t, users.reads, []githubRead{{Token: "ghu_alice", Method: "GetIssue", Args: []any{"o", "r", 7}}})
	result := f.llm.recordedInputs()[1].ToolResults[0]
	gt.False(t, result.IsError)
	gt.String(t, result.Content).Contains(`"Repository":"o/r"`)
	gt.Equal(t, f.bot.texts("UpdateProgress")[0], ":mag: Reading o/r#7")
	gt.String(t, f.llm.recordedInputs()[0].UserText).Contains("GitHub: connected\n")
}

func TestAgent_BudgetNoticeOnEveryCall(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(
		toolTurn(usd(0.10), toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		toolTurn(usd(0.20), toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
		textTurn("answer", usd(0.01)),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	inputs := f.llm.recordedInputs()
	gt.Array(t, inputs).Length(3).Required()
	gt.String(t, inputs[0].SystemNotice).Equal("Budget: $0.00 of $2.00 used, 0 of 20 model calls.")
	gt.String(t, inputs[1].SystemNotice).Equal("Budget: $0.10 of $2.00 used, 1 of 20 model calls.")
	gt.String(t, inputs[2].SystemNotice).Equal("Budget: $0.30 of $2.00 used, 2 of 20 model calls.")
	gt.String(t, inputs[0].UserText).Contains("<request")
	gt.Array(t, inputs[1].ToolResults).Length(1)
	gt.Array(t, inputs[2].ToolResults).Length(1)
}

func TestAgent_ConcludesNearTheCostLimit(t *testing.T) {
	f := newAgentFixture(t)
	// 95% of $2.00, partly in cache writes, which compaction usage adds to.
	f.llm.script(
		toolTurn(model.LLMUsage{InputTokens: 750_000, CacheCreationInputTokens: 160_000}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("short answer", usd(0.05)),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	inputs := f.llm.recordedInputs()
	gt.Array(t, inputs).Length(2).Required()
	gt.True(t, inputs[1].DisableTools)
	gt.String(t, inputs[1].SystemNotice).Equal("Budget: $1.90 of $2.00 used, 1 of 20 model calls.\n" +
		"The budget is nearly used up. Do not call tools. Write the final answer now from\n" +
		"the information you already have, and say what you could not check.")
	gt.Array(t, f.bot.answers()).Length(1)
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 2 LLM calls · 1 tool call · $1.95 · wrapped up near the cost limit")
	gt.Array(t, f.storedMessages(t)).Length(4)
}

func TestAgent_BelowTheNoticeRatioKeepsTools(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(
		toolTurn(usd(1.78), toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("answer", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	in := f.llm.recordedInputs()[1]
	gt.False(t, in.DisableTools)
	gt.False(t, strings.Contains(in.SystemNotice, "nearly used up"))
}

func TestAgent_StopsAtTheCostLimit(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(toolTurn(usd(2.40), toolCall("c1", "slack_search_messages", `{"query":"a"}`)))

	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	gt.Array(t, f.llm.recordedInputs()).Length(1)
	gt.Array(t, f.slackFactory.searches).Length(0)
	gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I reached the cost limit for one request ($2.00) before I could write an answer. Try a narrower request."})
	gt.String(t, f.lastProgress(t)).Equal(":money_with_wings: Stopped at the cost limit ($2.00) · 1 LLM call · 0 tool calls · $2.40")
	gt.Array(t, f.storedMessages(t)).Length(0)
}

func TestAgent_ConcludesAtTheCallLimit(t *testing.T) {
	f := newAgentFixture(t)
	f.cfg.MaxLLMCalls = 2
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		toolTurn(model.LLMUsage{}, model.LLMOutputBlock{Kind: model.LLMOutputText, Text: "final"}, toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	inputs := f.llm.recordedInputs()
	gt.Array(t, inputs).Length(2).Required()
	gt.True(t, inputs[1].DisableTools)
	gt.String(t, inputs[1].SystemNotice).Equal("Budget: $0.00 of $2.00 used, 1 of 2 model calls.\n" +
		"This is the last model call allowed for this request. Do not call tools. Write the final answer now from\n" +
		"the information you already have, and say what you could not check.")
	gt.Array(t, f.slackFactory.searches).Length(1)
	gt.Equal(t, f.bot.texts("PostAnswer"), []string{"final"})
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 2 LLM calls · 1 tool call · $0.00 · wrapped up at the model call limit")
}

func TestAgent_ConcludesAtTheHistorySizeLimit(t *testing.T) {
	f := newAgentFixture(t)
	// The fake model stores "in-1" and "out-1" (9 bytes) and the format
	// "fake.v1" twice (14 bytes) after the first call.
	f.cfg.HistoryByteLimit = 20
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("short answer", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	inputs := f.llm.recordedInputs()
	gt.Array(t, inputs).Length(2).Required()
	gt.False(t, inputs[0].DisableTools)
	gt.True(t, inputs[1].DisableTools)
	gt.String(t, inputs[1].SystemNotice).Contains("This request has gathered as much material as Robin can keep. Do not call tools.")
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 2 LLM calls · 1 tool call · $0.00 · wrapped up at the size limit of a conversation")
	gt.Array(t, f.storedMessages(t)).Length(4)
}

func TestAgent_ConcludingCallWithoutText(t *testing.T) {
	f := newAgentFixture(t)
	f.cfg.MaxLLMCalls = 2
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		toolTurn(model.LLMUsage{}, toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I couldn't write an answer to this request. Please ask again, or narrow it down."})
	gt.String(t, f.lastProgress(t)).Equal(":warning: No answer · 2 LLM calls · 1 tool call · $0.00")
	gt.Array(t, f.storedMessages(t)).Length(0)
}

func TestAgent_DeadlineStopsTheRemainingTools(t *testing.T) {
	f := newAgentFixture(t)
	f.cfg.Timeout = 20 * time.Millisecond
	f.slackFactory.searchWait = true
	f.llm.script(toolTurn(model.LLMUsage{},
		toolCall("c1", "slack_search_messages", `{"query":"a"}`),
		toolCall("c2", "slack_search_messages", `{"query":"b"}`),
		toolCall("c3", "slack_search_messages", `{"query":"c"}`),
	))

	gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))

	gt.Array(t, f.slackFactory.searches).Length(1)
	// The progress shows the first tool and then the end of the run only.
	gt.Equal(t, f.bot.texts("UpdateProgress"), []string{
		":mag: Searching Slack for “a”",
		":warning: Timed out after 20ms · 1 LLM call · 1 tool call · $0.00",
	})
	results := f.llm.recordedInputs()[1].ToolResults
	gt.Array(t, results).Length(3).Required()
	gt.String(t, results[1].Content).Equal("not run: the time limit of this request was reached")
	gt.String(t, results[2].Content).Equal("not run: the time limit of this request was reached")
	gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I couldn't finish this request within 20ms."})
}

func TestAgent_Refusal(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(fakeStep{turn: &model.LLMTurn{StopReason: model.LLMStopRefusal}})
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I can't help with this request."})
	gt.String(t, f.lastProgress(t)).Equal(":no_entry_sign: Declined · 1 LLM call · 0 tool calls · $0.00")
	gt.Array(t, f.storedMessages(t)).Length(0)
}

func TestAgent_MaxTokens(t *testing.T) {
	t.Run("text only", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.script(fakeStep{turn: &model.LLMTurn{
			Blocks:     []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: "long answer"}},
			StopReason: model.LLMStopMaxTokens,
		}})
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"long answer\n\n_(The answer was cut off because it was too long.)_"})
		gt.Array(t, f.storedMessages(t)).Length(2)
	})
	t.Run("with tool calls", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.script(
			fakeStep{turn: &model.LLMTurn{Blocks: []model.LLMOutputBlock{toolCall("c1", "slack_search_messages", `{"query":"a"}`)}, StopReason: model.LLMStopMaxTokens}},
			textTurn("answer", model.LLMUsage{}),
		)
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Array(t, f.slackFactory.searches).Length(1)
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"answer"})
	})
}

func TestAgent_Failures(t *testing.T) {
	t.Run("llm call", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.script(fakeStep{err: errors.New("overloaded")})
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I couldn't finish this request because of an internal error."})
		gt.String(t, f.lastProgress(t)).Equal(":warning: Failed · 0 LLM calls · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("restoring the history", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.newSessionErr = errors.New("broken history")
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I couldn't finish this request because of an internal error."})
		gt.Array(t, f.llm.recordedInputs()).Length(0)
		gt.Array(t, f.storedMessages(t)).Length(0) // storedMessages fails if the lease was kept
	})
	t.Run("history of another provider", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.newSessionErr = goerr.Wrap(interfaces.ErrLLMHistoryIncompatible, "other format")
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"This conversation was started with a different model setting and can't be continued. Mention me in a new thread."})
		gt.String(t, f.lastProgress(t)).Equal(":no_entry_sign: Can't continue this conversation")
		gt.Array(t, f.llm.recordedInputs()).Length(0)
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("timeout", func(t *testing.T) {
		f := newAgentFixture(t)
		f.cfg.Timeout = 10 * time.Millisecond
		f.llm.script(fakeStep{wait: true})
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I couldn't finish this request within 10ms."})
		gt.String(t, f.lastProgress(t)).Equal(":warning: Timed out after 10ms · 0 LLM calls · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("reading the thread", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.threadErr = errors.New("missing_scope")
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.Array(t, f.llm.recordedInputs()).Length(0)
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"I couldn't finish this request because of an internal error."})
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("reading the connections", func(t *testing.T) {
		f := newAgentFixture(t)
		f.repo = failingNotionRepository{Repository: f.repo}
		f.notion = usecase.NewNotionAccess(f.repo, f.cipher, newFakeNotionOAuth(), f.notionClients)
		f.services.Notion = f.notion
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.Array(t, f.llm.recordedInputs()).Length(0)
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("posting the answer", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.answerErr = errors.New("channel_not_found")
		f.llm.script(textTurn("answer", model.LLMUsage{}))
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.String(t, f.lastProgress(t)).Equal(":warning: Failed · 1 LLM call · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("saving the conversation", func(t *testing.T) {
		f := newAgentFixture(t)
		f.repo = failingCommitRepository{Repository: f.repo}
		f.llm.script(textTurn("answer", model.LLMUsage{}))
		gt.Error(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS)))
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"answer"})
		gt.String(t, f.lastProgress(t)).Equal(":warning: Answered, but this conversation could not be saved · 1 LLM call · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0) // the lease was released
	})
	t.Run("lease lost before the commit", func(t *testing.T) {
		f := newAgentFixture(t)
		f.repo = noCommitRepository{Repository: f.repo}
		f.llm.script(textTurn("answer", model.LLMUsage{}))
		err := f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))
		gt.Error(t, err).Is(usecase.ErrAgentSessionLeaseLost)
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"answer"})
		gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 1 LLM call · 0 tool calls · $0.00")
	})
}

// failingNotionRepository fails every read of a Notion credential.
type failingNotionRepository struct{ interfaces.Repository }

func (r failingNotionRepository) NotionCredential() interfaces.NotionCredentialRepository {
	return failingNotionCredentials{r.Repository.NotionCredential()}
}

type failingNotionCredentials struct {
	interfaces.NotionCredentialRepository
}

func (failingNotionCredentials) Get(context.Context, model.UserKey) (*model.NotionCredential, error) {
	return nil, errors.New("firestore unavailable")
}

// failingCommitRepository fails every commit, as Firestore does for a
// request above its size limit.
type failingCommitRepository struct{ interfaces.Repository }

func (r failingCommitRepository) AgentSession() interfaces.AgentSessionRepository {
	return failingCommitSessions{r.Repository.AgentSession()}
}

type failingCommitSessions struct {
	interfaces.AgentSessionRepository
}

func (failingCommitSessions) Commit(context.Context, model.UserKey, model.AgentSessionID, string, model.AgentSessionCommit) (bool, error) {
	return false, errors.New("request payload size exceeds the limit")
}

// noCommitRepository reports every commit as made without the lease.
type noCommitRepository struct{ interfaces.Repository }

func (r noCommitRepository) AgentSession() interfaces.AgentSessionRepository {
	return noCommitSessions{r.Repository.AgentSession()}
}

type noCommitSessions struct {
	interfaces.AgentSessionRepository
}

func (noCommitSessions) Commit(context.Context, model.UserKey, model.AgentSessionID, string, model.AgentSessionCommit) (bool, error) {
	return false, nil
}

func TestAgent_ToolInputErrors(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.script(
		toolTurn(model.LLMUsage{},
			toolCall("c1", "no_such_tool", `{}`),
			toolCall("c2", "slack_search_messages", `{"query":`),
			toolCall("c3", "slack_search_messages", `{}`),
			toolCall("c4", "slack_search_messages", `{"query":"a","count":50}`),
			toolCall("c5", "notion_get_page", `{"page_id":"not-a-uuid"}`),
			toolCall("c6", "calendar_list_events", `{"time_min":"tomorrow","time_max":"2026-10-05T00:00:00Z"}`),
		),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()

	results := f.llm.recordedInputs()[1].ToolResults
	gt.Array(t, results).Length(6).Required()
	for _, r := range results {
		gt.True(t, r.IsError)
	}
	gt.String(t, results[0].Content).Equal("unknown tool: no_such_tool")
	gt.String(t, results[1].Content).Contains("Invalid input: ")
	gt.String(t, results[2].Content).Equal("Invalid input: query is required.")
	gt.String(t, results[3].Content).Equal("Invalid input: count must be between 1 and 20.")
	gt.String(t, results[4].Content).Equal("Invalid input: page_id must be a Notion ID (a UUID).")
	gt.String(t, results[5].Content).Equal("Invalid input: time_min must be an RFC 3339 time such as 2026-10-04T09:00:00+09:00.")
	gt.Array(t, f.slackFactory.searches).Length(0)
}

func TestAgent_ToolErrorsForTheModel(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"not found":      {err: goerr.Wrap(interfaces.ErrNotionNotFound, "404"), want: "Not found, or not shared with this user."},
		"rate limited":   {err: goerr.Wrap(interfaces.ErrNotionRateLimited, "429"), want: "Rate limited by Notion. Try again later."},
		"other":          {err: errors.New("connection reset by 10.0.0.1"), want: "Internal error."},
		"token rejected": {err: goerr.Wrap(interfaces.ErrNotionTokenInvalid, "401"), want: "Notion rejected the stored connection. The user has to reconnect it at https://robin.example.com/settings."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAgentFixture(t)
			f.notionClients.errs["notion-access"] = tc.err
			// The refresh after a rejected token is rejected too.
			oauth := newFakeNotionOAuth()
			oauth.refreshErr = goerr.Wrap(interfaces.ErrNotionTokenInvalid, "invalid_grant")
			f.notion = usecase.NewNotionAccess(f.repo, f.cipher, oauth, f.notionClients)
			f.services.Notion = f.notion
			f.llm.script(
				toolTurn(model.LLMUsage{}, toolCall("c1", "notion_get_page", `{"page_id":"0f4a2b1c-3d4e-4f50-8a6b-7c8d9e0f1a2b"}`)),
				textTurn("done", model.LLMUsage{}),
			)
			gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
			result := f.llm.recordedInputs()[1].ToolResults[0]
			gt.True(t, result.IsError)
			gt.String(t, result.Content).Equal(tc.want)
		})
	}
}

func TestAgent_ToolResultIsTruncated(t *testing.T) {
	f := newAgentFixture(t)
	f.slackFactory.hits = []model.SlackMessageHit{{ChannelID: "C1", ChannelType: "channel", Text: strings.Repeat("a", 25000)}}
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	content := f.llm.recordedInputs()[1].ToolResults[0].Content
	head, note, found := strings.Cut(content, "\n[truncated: ")
	gt.True(t, found)
	gt.Number(t, len([]rune(head))).Equal(20000)
	gt.True(t, strings.HasSuffix(note, " more characters]"))
}

func TestAgent_SlackSearchResult(t *testing.T) {
	f := newAgentFixture(t)
	f.slackFactory.hits = []model.SlackMessageHit{
		{ChannelID: "D1", ChannelName: "U9", ChannelType: "im", UserID: "U9", Username: "bob", TS: "1.1", Text: "secret", Permalink: "https://example.slack.com/p1"},
	}
	f.llm.script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"plan","count":5}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
	gt.Equal(t, f.slackFactory.searches, []slackSearch{{Token: testUserToken, Query: "plan", Count: 5}})
	gt.String(t, f.llm.recordedInputs()[1].ToolResults[0].Content).Equal(
		`[{"channel_id":"D1","channel_name":"U9","channel_type":"im","user_id":"U9","username":"bob","ts":"1.1","text":"secret","permalink":"https://example.slack.com/p1"}]`)
}

func TestAgent_SessionStates(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		f := newAgentFixture(t)
		_, err := f.repo.AgentSession().Begin(context.Background(), testKey, model.AgentSessionBeginRequest{
			ID: f.sessionID(t), ChannelID: testChannel, ThreadTS: agentThreadTS, LeaseID: "other-run",
			Now: f.now, LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "g",
		})
		gt.NoError(t, err).Required()
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Equal(t, f.bot.texts("UpdateProgress"), []string{":hourglass_flowing_sand: Still working on your previous request in this thread"})
		gt.Array(t, f.llm.configs).Length(0)
		gt.Array(t, f.bot.recorded("PostAnswer")).Length(0)
	})
	t.Run("owned by another user", func(t *testing.T) {
		f := newAgentFixture(t)
		other := model.UserKey{TeamID: testKey.TeamID, UserID: "U0999ZZZZ"}
		_, err := f.repo.AgentSession().Begin(context.Background(), other, model.AgentSessionBeginRequest{
			ID: f.sessionID(t), ChannelID: testChannel, ThreadTS: agentThreadTS, LeaseID: "other-run",
			Now: f.now, LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "g",
		})
		gt.NoError(t, err).Required()
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Equal(t, f.bot.texts("UpdateProgress"), []string{":no_entry_sign: Only the person who started this conversation can continue it"})
		gt.Equal(t, f.bot.ephemeralMessages(), []botMessage{{ChannelID: testChannel, UserID: testKey.UserID, ThreadTS: agentThreadTS,
			Text: "In this thread, I only answer the person who started the conversation with me. Mention me in a new thread to start your own."}})
		gt.Array(t, f.llm.configs).Length(0)
	})
}

func TestAgent_ProgressFailures(t *testing.T) {
	t.Run("update fails", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.updateErr = errors.New("ratelimited")
		f.llm.script(
			toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
			textTurn("answer", model.LLMUsage{}),
		)
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Array(t, f.bot.recorded("UpdateProgress")).Length(2)
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"answer"})
		gt.Array(t, f.storedMessages(t)).Length(4)
	})
	t.Run("progress message deleted", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.updateErr = goerr.Wrap(interfaces.ErrSlackMessageNotFound, "message_not_found")
		f.llm.script(
			toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
			toolTurn(model.LLMUsage{}, toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
			textTurn("answer", model.LLMUsage{}),
		)
		gt.NoError(t, f.agent(t).Run(context.Background(), f.request(t, agentMentionTS))).Required()
		gt.Array(t, f.bot.recorded("UpdateProgress")).Length(1)
		gt.Equal(t, f.bot.texts("PostAnswer"), []string{"answer"})
		gt.Array(t, f.storedMessages(t)).Length(6)
	})
}

func TestNewAgent_InvalidConfig(t *testing.T) {
	cases := map[string]func(c *usecase.AgentConfig){
		"no base URL":       func(c *usecase.AgentConfig) { c.BaseURL = "" },
		"zero budget":       func(c *usecase.AgentConfig) { c.Budget = 0 },
		"ratio of one":      func(c *usecase.AgentConfig) { c.NoticeRatio = 1 },
		"one model call":    func(c *usecase.AgentConfig) { c.MaxLLMCalls = 1 },
		"no tool calls":     func(c *usecase.AgentConfig) { c.MaxToolCalls = 0 },
		"no timeout":        func(c *usecase.AgentConfig) { c.Timeout = 0 },
		"no session TTL":    func(c *usecase.AgentConfig) { c.SessionTTL = 0 },
		"no result limit":   func(c *usecase.AgentConfig) { c.ToolResultLimit = 0 },
		"zero output price": func(c *usecase.AgentConfig) { c.Rate.Output = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testAgentConfig()
			mutate(&cfg)
			_, err := usecase.NewAgent(memory.New(), &fakeLLM{}, newFakeBot(), usecase.AgentServices{}, cfg)
			gt.Error(t, err)
		})
	}
}
