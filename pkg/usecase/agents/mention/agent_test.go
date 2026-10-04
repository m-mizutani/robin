package mention_test

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
	"github.com/m-mizutani/robin/pkg/usecase/agents/mention"
	"github.com/m-mizutani/robin/pkg/usecase/usecasetest"
)

var (
	textTurn = usecasetest.TextTurn
	toolTurn = usecasetest.ToolTurn
	toolCall = usecasetest.ToolCall
)

// usd returns a usage that costs about the given dollars at the test rate
// ($2 per million input tokens).
func usd(dollars float64) model.LLMUsage {
	return model.LLMUsage{InputTokens: int64(dollars * 500_000)}
}

const (
	testBaseURL     = "https://robin.example.com"
	testChannel     = "C0123ABCD"
	agentThreadTS   = "1700000000.000100"
	agentMentionTS  = "1700000100.000100"
	agentMentionTS2 = "1700000200.000100"
	agentProgressTS = "1800000000.000001"
)

var testKey = model.UserKey{TeamID: "T0123ABCD", UserID: "U0123ABCD"}

type slackSearch struct {
	Query string
	Count int
}

// fakeSlack is the requester's Slack client. Wait makes a search block until
// its context ends.
type fakeSlack struct {
	mu       sync.Mutex
	hits     []model.SlackMessageHit
	wait     bool
	searches []slackSearch
}

func (f *fakeSlack) AuthTest(context.Context) (*model.SlackIdentity, error) {
	return nil, errors.New("auth.test is not used by the agent")
}

func (f *fakeSlack) SearchMessages(ctx context.Context, query string, count int) ([]model.SlackMessageHit, error) {
	if f.wait {
		<-ctx.Done()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, slackSearch{Query: query, Count: count})
	if f.wait {
		return nil, ctx.Err()
	}
	return f.hits, nil
}

func (f *fakeSlack) recorded() []slackSearch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]slackSearch(nil), f.searches...)
}

// notionCall is one read the fake Notion received.
type notionCall struct {
	Method string
	ID     model.NotionObjectID
	Input  any
}

// fakeNotion answers every read with fixed results, or with err.
type fakeNotion struct {
	mu        sync.Mutex
	status    usecase.NotionStatus
	statusErr error
	err       error
	calls     []notionCall
}

var (
	notionListResult = &model.NotionList{Results: []json.RawMessage{json.RawMessage(`{"object":"page","id":"p1"}`)}, NextCursor: "c2", HasMore: true}
	notionObject     = json.RawMessage(`{"object":"page","id":"p1"}`)
)

func (f *fakeNotion) record(c notionCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.err
}

func (f *fakeNotion) Connection(context.Context, model.UserKey) (*usecase.NotionStatus, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	st := f.status
	return &st, nil
}

func (f *fakeNotion) list(c notionCall) (*model.NotionList, error) {
	if err := f.record(c); err != nil {
		return nil, err
	}
	return notionListResult, nil
}

func (f *fakeNotion) object(c notionCall) (json.RawMessage, error) {
	if err := f.record(c); err != nil {
		return nil, err
	}
	return notionObject, nil
}

func (f *fakeNotion) Search(_ context.Context, _ model.UserKey, q model.NotionSearchQuery) (*model.NotionList, error) {
	return f.list(notionCall{Method: "Search", Input: q})
}

func (f *fakeNotion) GetPage(_ context.Context, _ model.UserKey, id model.NotionObjectID) (json.RawMessage, error) {
	return f.object(notionCall{Method: "GetPage", ID: id})
}

func (f *fakeNotion) ListBlockChildren(_ context.Context, _ model.UserKey, id model.NotionObjectID, page model.NotionPagination) (*model.NotionList, error) {
	return f.list(notionCall{Method: "ListBlockChildren", ID: id, Input: page})
}

func (f *fakeNotion) GetDatabase(_ context.Context, _ model.UserKey, id model.NotionObjectID) (json.RawMessage, error) {
	return f.object(notionCall{Method: "GetDatabase", ID: id})
}

func (f *fakeNotion) GetDataSource(_ context.Context, _ model.UserKey, id model.NotionObjectID) (json.RawMessage, error) {
	return f.object(notionCall{Method: "GetDataSource", ID: id})
}

func (f *fakeNotion) QueryDataSource(_ context.Context, _ model.UserKey, id model.NotionObjectID, q model.NotionDataSourceQuery) (*model.NotionList, error) {
	return f.list(notionCall{Method: "QueryDataSource", ID: id, Input: q})
}

// fakeGoogle is a Google Workspace that is not connected: every read fails
// as GoogleWorkspaceAccess does without a stored token.
type fakeGoogle struct{}

var errGoogleNotConnected = goerr.Wrap(usecase.ErrGoogleWorkspaceNotConnected, "no google workspace credential")

func (fakeGoogle) Connection(context.Context, model.UserKey) (*usecase.GoogleWorkspaceStatus, error) {
	return &usecase.GoogleWorkspaceStatus{}, nil
}

func (fakeGoogle) SearchGmail(context.Context, model.UserKey, model.GmailSearchQuery) ([]model.GmailMessageSummary, error) {
	return nil, errGoogleNotConnected
}

func (fakeGoogle) GetGmailMessage(context.Context, model.UserKey, string) (*model.GmailMessage, error) {
	return nil, errGoogleNotConnected
}

func (fakeGoogle) SearchDrive(context.Context, model.UserKey, model.DriveSearchQuery) ([]model.DriveFile, error) {
	return nil, errGoogleNotConnected
}

func (fakeGoogle) GetDriveFileText(context.Context, model.UserKey, string) (*model.DriveFileText, error) {
	return nil, errGoogleNotConnected
}

func (fakeGoogle) ListCalendarEvents(context.Context, model.UserKey, model.CalendarEventQuery) ([]model.CalendarEvent, error) {
	return nil, errGoogleNotConnected
}

// fakeGitHub is a connected GitHub whose client records the issues it reads.
type fakeGitHub struct {
	mu     sync.Mutex
	issues []string
}

func (f *fakeGitHub) Connection(context.Context, model.UserKey) (*usecase.GitHubStatus, error) {
	return &usecase.GitHubStatus{Connected: true, Login: "alice"}, nil
}

func (f *fakeGitHub) Client(context.Context, model.UserKey) (interfaces.GitHubUserClient, error) {
	return &fakeGitHubClient{github: f}, nil
}

type fakeGitHubClient struct {
	interfaces.GitHubUserClient // only GetIssue is used
	github                      *fakeGitHub
}

func (c *fakeGitHubClient) GetIssue(_ context.Context, owner, repo string, number int) (*model.GitHubIssue, error) {
	c.github.mu.Lock()
	defer c.github.mu.Unlock()
	c.github.issues = append(c.github.issues, fmt.Sprintf("%s/%s#%d", owner, repo, number))
	return &model.GitHubIssue{GitHubIssueSummary: model.GitHubIssueSummary{Repository: owner + "/" + repo, Number: number}}, nil
}

type agentFixture struct {
	repo     interfaces.Repository
	bot      *usecasetest.SlackBot
	llm      *usecasetest.LLM
	slack    *fakeSlack
	notion   *fakeNotion
	services mention.Services
	cfg      mention.Config
	now      time.Time
	ids      int
}

func testConfig() mention.Config {
	return mention.Config{
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

// newAgentFixture connects Notion, leaves Google Workspace unconnected and
// disables GitHub.
func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	f := &agentFixture{
		repo:   memory.New(),
		bot:    usecasetest.NewSlackBot(),
		llm:    &usecasetest.LLM{},
		slack:  &fakeSlack{},
		notion: &fakeNotion{status: usecase.NotionStatus{Connected: true}},
		cfg:    testConfig(),
		now:    time.Date(2026, 10, 4, 1, 7, 2, 0, time.UTC),
	}
	f.services = mention.Services{Notion: f.notion, Google: fakeGoogle{}}
	return f
}

func (f *agentFixture) agent(t *testing.T) *mention.Agent {
	t.Helper()
	a, err := mention.New(f.repo, f.llm, f.bot, f.services, f.cfg)
	gt.NoError(t, err).Required()
	a.SetClockForTest(func() time.Time { return f.now }, func() string {
		f.ids++
		return fmt.Sprintf("id-%d", f.ids)
	})
	return a
}

func (f *agentFixture) request(mentionTS string) usecase.MentionRequest {
	return usecase.MentionRequest{
		Key:        testKey,
		Slack:      f.slack,
		ChannelID:  testChannel,
		ThreadTS:   agentThreadTS,
		MentionTS:  mentionTS,
		InThread:   true,
		Text:       "<@UROBIN> what is the plan?",
		ProgressTS: agentProgressTS,
	}
}

func (f *agentFixture) run(t *testing.T, mentionTS string) error {
	t.Helper()
	return f.agent(t).Run(context.Background(), f.request(mentionTS))
}

func sessionID(t *testing.T) model.AgentSessionID {
	t.Helper()
	id, err := model.NewAgentSessionID(testKey.TeamID, testChannel, agentThreadTS)
	gt.NoError(t, err).Required()
	return id
}

// storedMessages returns the messages of the session's current generation.
// It fails when a run left the lease behind.
func (f *agentFixture) storedMessages(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	res, err := f.repo.AgentSession().Begin(ctx, testKey, model.AgentSessionBeginRequest{
		ID: sessionID(t), ChannelID: testChannel, ThreadTS: agentThreadTS, LeaseID: "check",
		Now: f.now, LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "check-gen",
	})
	gt.NoError(t, err).Required()
	gt.Value(t, res.Status).NotEqual(model.AgentSessionBusy).Required()
	msgs, err := f.repo.AgentSession().ListMessages(ctx, testKey, sessionID(t), res.Session.Generation)
	gt.NoError(t, err).Required()
	gt.NoError(t, f.repo.AgentSession().Release(ctx, testKey, sessionID(t), "check")).Required()
	var out []string
	for _, m := range msgs {
		out = append(out, string(m.Data))
	}
	return out
}

func (f *agentFixture) lastProgress(t *testing.T) string {
	t.Helper()
	updates := f.bot.Texts("UpdateProgress")
	gt.Array(t, updates).Longer(0).Required()
	return updates[len(updates)-1]
}

func TestAgent_FirstMentionWithoutTools(t *testing.T) {
	f := newAgentFixture(t)
	f.bot.Thread = []model.SlackThreadMessage{
		{TS: "1700000000.000100", UserID: "U0123ABCD", Text: "the plan for Q4"},
		{TS: "1700000050.000100", BotID: "B0999", Text: "deploy finished"},
	}
	f.llm.Script(textTurn("Here is the plan.", model.LLMUsage{InputTokens: 1000, OutputTokens: 100}))

	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	inputs := f.llm.Inputs()
	gt.Array(t, inputs).Length(1).Required()
	gt.String(t, inputs[0].UserText).Equal("<thread>\n" +
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

	gt.Equal(t, f.bot.Answers(), []usecasetest.SlackMessage{{ChannelID: testChannel, ThreadTS: agentThreadTS, Text: "Here is the plan."}})
	gt.Equal(t, f.bot.Texts("GetThreadMessages"), []string{"after= before=" + agentMentionTS + " limit=50"})
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 1 LLM call · 0 tool calls · $0.00")
	gt.Equal(t, f.storedMessages(t), []string{"in-1", "out-1"})
	for _, c := range f.bot.Recorded("PostAnswer", "UpdateProgress") {
		gt.Value(t, c.Requester).Equal(testKey.UserID)
	}
	for _, c := range f.bot.Recorded("UpdateProgress") {
		gt.String(t, c.TS).Equal(agentProgressTS)
	}
}

func TestAgent_SecondMentionContinuesTheConversation(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(textTurn("first answer", model.LLMUsage{}), textTurn("second answer", model.LLMUsage{}))
	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	f.bot.Thread = []model.SlackThreadMessage{
		{TS: "1700000150.000100", BotID: "B1", FromRobin: true, Text: "first answer"},
		{TS: "1700000160.000100", UserID: "U0999", Text: "a comment from someone else"},
	}
	gt.NoError(t, f.run(t, agentMentionTS2)).Required()

	histories := f.llm.Histories()
	gt.Array(t, histories).Length(2).Required()
	gt.Array(t, histories[0]).Length(0)
	gt.Equal(t, histories[1], []model.LLMHistoryMessage{
		{Format: "fake.v1", Data: []byte("in-1")}, {Format: "fake.v1", Data: []byte("out-1")},
	})
	gt.Equal(t, f.bot.Texts("GetThreadMessages")[1], "after="+agentMentionTS+" before="+agentMentionTS2+" limit=50")
	second := f.llm.Inputs()[1].UserText
	gt.String(t, second).Contains("<@U0999>: a comment from someone else")
	gt.False(t, strings.Contains(second, "first answer"))
	gt.Equal(t, f.storedMessages(t), []string{"in-1", "out-1", "in-2", "out-2"})
}

func TestAgent_FirstMentionAtTheTopLevelReadsNoThread(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(textTurn("answer", model.LLMUsage{}))
	req := f.request(agentThreadTS)
	req.InThread = false
	gt.NoError(t, f.agent(t).Run(context.Background(), req)).Required()
	gt.Array(t, f.bot.Recorded("GetThreadMessages")).Length(0)
	gt.False(t, strings.Contains(f.llm.Inputs()[0].UserText, "<thread>"))
}

func TestAgent_ToolUse(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(
		toolTurn(model.LLMUsage{InputTokens: 1000},
			model.LLMOutputBlock{Kind: model.LLMOutputProgress, Text: "I should look in Notion."},
			toolCall("call-1", "notion_search", `{"query":"q4 plan"}`)),
		textTurn("The plan is X.", model.LLMUsage{InputTokens: 1000}),
	)

	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	gt.Equal(t, f.bot.Texts("UpdateProgress"), []string{
		":thought_balloon: I should look in Notion.",
		":mag: Searching Notion for “q4 plan”",
		":white_check_mark: Done · 2 LLM calls · 1 tool call · $0.00",
	})
	gt.Equal(t, f.notion.calls, []notionCall{{Method: "Search", Input: model.NotionSearchQuery{Query: "q4 plan", Page: model.NotionPagination{PageSize: 25}}}})

	inputs := f.llm.Inputs()
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
	f.llm.Script(toolTurn(model.LLMUsage{}, blocks...), textTurn("done", model.LLMUsage{}))

	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	gt.Array(t, f.slack.recorded()).Length(10)
	results := f.llm.Inputs()[1].ToolResults
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
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("call-1", "gmail_search", `{"query":"from:bob"}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	configs := f.llm.Configs()
	var names []string
	for _, spec := range configs[0].Tools {
		names = append(names, spec.Name)
	}
	gt.Equal(t, names, []string{
		"slack_search_messages",
		"notion_search", "notion_get_page", "notion_get_block_children", "notion_get_database", "notion_get_data_source", "notion_query_data_source",
		"gmail_search", "gmail_get_message", "drive_search", "drive_get_file", "calendar_list_events",
	})
	gt.String(t, f.llm.Inputs()[0].UserText).Contains("Google Workspace: not connected (connect at https://robin.example.com/settings)")
	result := f.llm.Inputs()[1].ToolResults[0]
	gt.True(t, result.IsError)
	gt.String(t, result.Content).Equal("Google Workspace is not connected. The user can connect it at https://robin.example.com/settings.")

	// Another user with other connections gets the same prompt and tools.
	other := newAgentFixture(t)
	other.notion.status = usecase.NotionStatus{}
	other.llm.Script(textTurn("hi", model.LLMUsage{}))
	gt.NoError(t, other.run(t, agentMentionTS)).Required()
	gt.Equal(t, other.llm.Configs()[0], configs[0])

	prompt := configs[0].SystemPrompt
	gt.String(t, prompt).Contains("Slack messages, Notion and Google Workspace (Gmail, Drive and Calendar)")
	gt.String(t, prompt).Contains("https://robin.example.com/settings")
	gt.String(t, prompt).Contains(`channel_type is "group" (a private channel), "im" (a DM) or "mpim" (a group DM)`)
	gt.String(t, prompt).Contains("Do not quote or copy the text of those messages into your answer")
	gt.String(t, prompt).Contains(`A system message that starts with "Budget:"`)
}

func TestAgent_GitHubTools(t *testing.T) {
	f := newAgentFixture(t)
	github := &fakeGitHub{}
	f.services.GitHub = github
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "github_get_issue", `{"owner":"o","repo":"r","number":7}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	gt.Equal(t, github.issues, []string{"o/r#7"})
	result := f.llm.Inputs()[1].ToolResults[0]
	gt.False(t, result.IsError)
	gt.String(t, result.Content).Contains(`"Repository":"o/r"`)
	gt.Equal(t, f.bot.Texts("UpdateProgress")[0], ":mag: Reading o/r#7")
	gt.String(t, f.llm.Inputs()[0].UserText).Contains("GitHub: connected\n")
	gt.Array(t, f.llm.Configs()[0].Tools).Length(16)
}

func TestAgent_BudgetNoticeOnEveryCall(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(
		toolTurn(usd(0.10), toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		toolTurn(usd(0.20), toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
		textTurn("answer", usd(0.01)),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	inputs := f.llm.Inputs()
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
	f.llm.Script(
		toolTurn(model.LLMUsage{InputTokens: 750_000, CacheCreationInputTokens: 160_000}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("short answer", usd(0.05)),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	inputs := f.llm.Inputs()
	gt.Array(t, inputs).Length(2).Required()
	gt.True(t, inputs[1].DisableTools)
	gt.String(t, inputs[1].SystemNotice).Equal("Budget: $1.90 of $2.00 used, 1 of 20 model calls.\n" +
		"The budget is nearly used up. Do not call tools. Write the final answer now from\n" +
		"the information you already have, and say what you could not check.")
	gt.Array(t, f.bot.Answers()).Length(1)
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 2 LLM calls · 1 tool call · $1.95 · wrapped up near the cost limit")
	gt.Array(t, f.storedMessages(t)).Length(4)
}

func TestAgent_BelowTheNoticeRatioKeepsTools(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(
		toolTurn(usd(1.78), toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("answer", model.LLMUsage{}),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	in := f.llm.Inputs()[1]
	gt.False(t, in.DisableTools)
	gt.False(t, strings.Contains(in.SystemNotice, "nearly used up"))
}

func TestAgent_StopsAtTheCostLimit(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(toolTurn(usd(2.40), toolCall("c1", "slack_search_messages", `{"query":"a"}`)))

	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	gt.Array(t, f.llm.Inputs()).Length(1)
	gt.Array(t, f.slack.recorded()).Length(0)
	gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"I reached the cost limit for one request ($2.00) before I could write an answer. Try a narrower request."})
	gt.String(t, f.lastProgress(t)).Equal(":money_with_wings: Stopped at the cost limit ($2.00) · 1 LLM call · 0 tool calls · $2.40")
	gt.Array(t, f.storedMessages(t)).Length(0)
}

func TestAgent_ConcludesAtTheCallLimit(t *testing.T) {
	f := newAgentFixture(t)
	f.cfg.MaxLLMCalls = 2
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		toolTurn(model.LLMUsage{}, model.LLMOutputBlock{Kind: model.LLMOutputText, Text: "final"}, toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	inputs := f.llm.Inputs()
	gt.Array(t, inputs).Length(2).Required()
	gt.True(t, inputs[1].DisableTools)
	gt.String(t, inputs[1].SystemNotice).Equal("Budget: $0.00 of $2.00 used, 1 of 2 model calls.\n" +
		"This is the last model call allowed for this request. Do not call tools. Write the final answer now from\n" +
		"the information you already have, and say what you could not check.")
	gt.Array(t, f.slack.recorded()).Length(1)
	gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"final"})
	gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 2 LLM calls · 1 tool call · $0.00 · wrapped up at the model call limit")
}

func TestAgent_ConcludesAtTheHistorySizeLimit(t *testing.T) {
	f := newAgentFixture(t)
	// The fake model stores "in-1" and "out-1" (9 bytes) and the format
	// "fake.v1" twice (14 bytes) after the first call.
	f.cfg.HistoryByteLimit = 20
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("short answer", model.LLMUsage{}),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	inputs := f.llm.Inputs()
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
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		toolTurn(model.LLMUsage{}, toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"I couldn't write an answer to this request. Please ask again, or narrow it down."})
	gt.String(t, f.lastProgress(t)).Equal(":warning: No answer · 2 LLM calls · 1 tool call · $0.00")
	gt.Array(t, f.storedMessages(t)).Length(0)
}

func TestAgent_DeadlineStopsTheRemainingTools(t *testing.T) {
	f := newAgentFixture(t)
	f.cfg.Timeout = 20 * time.Millisecond
	f.slack.wait = true
	f.llm.Script(toolTurn(model.LLMUsage{},
		toolCall("c1", "slack_search_messages", `{"query":"a"}`),
		toolCall("c2", "slack_search_messages", `{"query":"b"}`),
		toolCall("c3", "slack_search_messages", `{"query":"c"}`),
	))

	gt.Error(t, f.run(t, agentMentionTS))

	gt.Array(t, f.slack.recorded()).Length(1)
	// The progress shows the first tool and then the end of the run only.
	gt.Equal(t, f.bot.Texts("UpdateProgress"), []string{
		":mag: Searching Slack for “a”",
		":warning: Timed out after 20ms · 1 LLM call · 1 tool call · $0.00",
	})
	results := f.llm.Inputs()[1].ToolResults
	gt.Array(t, results).Length(3).Required()
	gt.String(t, results[1].Content).Equal("not run: the time limit of this request was reached")
	gt.String(t, results[2].Content).Equal("not run: the time limit of this request was reached")
	gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"I couldn't finish this request within 20ms."})
}

func TestAgent_Refusal(t *testing.T) {
	f := newAgentFixture(t)
	f.llm.Script(usecasetest.LLMStep{Turn: &model.LLMTurn{StopReason: model.LLMStopRefusal}})
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"I can't help with this request."})
	gt.String(t, f.lastProgress(t)).Equal(":no_entry_sign: Declined · 1 LLM call · 0 tool calls · $0.00")
	gt.Array(t, f.storedMessages(t)).Length(0)
}

func TestAgent_MaxTokens(t *testing.T) {
	t.Run("text only", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.Script(usecasetest.LLMStep{Turn: &model.LLMTurn{
			Blocks:     []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: "long answer"}},
			StopReason: model.LLMStopMaxTokens,
		}})
		gt.NoError(t, f.run(t, agentMentionTS)).Required()
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"long answer\n\n_(The answer was cut off because it was too long.)_"})
		gt.Array(t, f.storedMessages(t)).Length(2)
	})
	t.Run("with tool calls", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.Script(
			usecasetest.LLMStep{Turn: &model.LLMTurn{Blocks: []model.LLMOutputBlock{toolCall("c1", "slack_search_messages", `{"query":"a"}`)}, StopReason: model.LLMStopMaxTokens}},
			textTurn("answer", model.LLMUsage{}),
		)
		gt.NoError(t, f.run(t, agentMentionTS)).Required()
		gt.Array(t, f.slack.recorded()).Length(1)
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"answer"})
	})
}

const failedReply = "I couldn't finish this request because of an internal error."

func TestAgent_Failures(t *testing.T) {
	t.Run("llm call", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.Script(usecasetest.LLMStep{Err: errors.New("overloaded")})
		gt.Error(t, f.run(t, agentMentionTS))
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{failedReply})
		gt.String(t, f.lastProgress(t)).Equal(":warning: Failed · 0 LLM calls · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("restoring the history", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.NewSessionErr = errors.New("broken history")
		gt.Error(t, f.run(t, agentMentionTS))
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{failedReply})
		gt.Array(t, f.llm.Inputs()).Length(0)
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("history of another provider", func(t *testing.T) {
		f := newAgentFixture(t)
		f.llm.NewSessionErr = goerr.Wrap(interfaces.ErrLLMHistoryIncompatible, "other format")
		gt.NoError(t, f.run(t, agentMentionTS)).Required()
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"This conversation was started with a different model setting and can't be continued. Mention me in a new thread."})
		gt.String(t, f.lastProgress(t)).Equal(":no_entry_sign: Can't continue this conversation")
		gt.Array(t, f.llm.Inputs()).Length(0)
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("timeout", func(t *testing.T) {
		f := newAgentFixture(t)
		f.cfg.Timeout = 10 * time.Millisecond
		f.llm.Script(usecasetest.LLMStep{Wait: true})
		gt.Error(t, f.run(t, agentMentionTS))
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"I couldn't finish this request within 10ms."})
		gt.String(t, f.lastProgress(t)).Equal(":warning: Timed out after 10ms · 0 LLM calls · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("reading the thread", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.ThreadErr = errors.New("missing_scope")
		gt.Error(t, f.run(t, agentMentionTS))
		gt.Array(t, f.llm.Inputs()).Length(0)
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{failedReply})
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("reading the connections", func(t *testing.T) {
		f := newAgentFixture(t)
		f.notion.statusErr = errors.New("firestore unavailable")
		gt.Error(t, f.run(t, agentMentionTS))
		gt.Array(t, f.llm.Inputs()).Length(0)
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{failedReply})
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("posting the answer", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.AnswerErr = errors.New("channel_not_found")
		f.llm.Script(textTurn("answer", model.LLMUsage{}))
		gt.Error(t, f.run(t, agentMentionTS))
		gt.String(t, f.lastProgress(t)).Equal(":warning: Failed · 1 LLM call · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0)
	})
	t.Run("saving the conversation", func(t *testing.T) {
		f := newAgentFixture(t)
		f.repo = failingCommitRepository{Repository: f.repo}
		f.llm.Script(textTurn("answer", model.LLMUsage{}))
		gt.Error(t, f.run(t, agentMentionTS))
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"answer"})
		gt.String(t, f.lastProgress(t)).Equal(":warning: Answered, but this conversation could not be saved · 1 LLM call · 0 tool calls · $0.00")
		gt.Array(t, f.storedMessages(t)).Length(0) // the lease was released
	})
	t.Run("lease lost before the commit", func(t *testing.T) {
		f := newAgentFixture(t)
		f.repo = noCommitRepository{Repository: f.repo}
		f.llm.Script(textTurn("answer", model.LLMUsage{}))
		gt.Error(t, f.run(t, agentMentionTS)).Is(mention.ErrSessionLeaseLost)
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"answer"})
		gt.String(t, f.lastProgress(t)).Equal(":white_check_mark: Done · 1 LLM call · 0 tool calls · $0.00")
	})
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
	f.llm.Script(
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
	gt.NoError(t, f.run(t, agentMentionTS)).Required()

	results := f.llm.Inputs()[1].ToolResults
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
	gt.Array(t, f.slack.recorded()).Length(0)
	gt.Array(t, f.notion.calls).Length(0)
}

func TestAgent_ToolErrorsForTheModel(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"not found":       {err: goerr.Wrap(interfaces.ErrNotionNotFound, "404"), want: "Not found, or not shared with this user."},
		"rate limited":    {err: goerr.Wrap(interfaces.ErrNotionRateLimited, "429"), want: "Rate limited by Notion. Try again later."},
		"invalid request": {err: goerr.Wrap(usecase.ErrNotionInvalidRequest, "bad filter"), want: "Invalid request."},
		"not connected":   {err: goerr.Wrap(usecase.ErrNotionNotConnected, "none"), want: "Notion is not connected. The user can connect it at https://robin.example.com/settings."},
		"reconnect":       {err: goerr.Wrap(usecase.ErrNotionReconnectRequired, "refresh rejected"), want: "Notion rejected the stored connection. The user has to reconnect it at https://robin.example.com/settings."},
		"unclassified":    {err: errors.New("connection reset by 10.0.0.1"), want: "Internal error."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAgentFixture(t)
			f.notion.err = tc.err
			f.llm.Script(
				toolTurn(model.LLMUsage{}, toolCall("c1", "notion_get_page", `{"page_id":"0f4a2b1c-3d4e-4f50-8a6b-7c8d9e0f1a2b"}`)),
				textTurn("done", model.LLMUsage{}),
			)
			gt.NoError(t, f.run(t, agentMentionTS)).Required()
			result := f.llm.Inputs()[1].ToolResults[0]
			gt.True(t, result.IsError)
			gt.String(t, result.Content).Equal(tc.want)
		})
	}
}

func TestAgent_ToolResultIsTruncated(t *testing.T) {
	f := newAgentFixture(t)
	f.slack.hits = []model.SlackMessageHit{{ChannelID: "C1", ChannelType: "channel", Text: strings.Repeat("a", 25000)}}
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	content := f.llm.Inputs()[1].ToolResults[0].Content
	head, note, found := strings.Cut(content, "\n[truncated: ")
	gt.True(t, found)
	gt.Number(t, len([]rune(head))).Equal(20000)
	gt.True(t, strings.HasSuffix(note, " more characters]"))
}

func TestAgent_SlackSearchResult(t *testing.T) {
	f := newAgentFixture(t)
	f.slack.hits = []model.SlackMessageHit{
		{ChannelID: "D1", ChannelName: "U9", ChannelType: "im", UserID: "U9", Username: "bob", TS: "1.1", Text: "secret", Permalink: "https://example.slack.com/p1"},
	}
	f.llm.Script(
		toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"plan","count":5}`)),
		textTurn("done", model.LLMUsage{}),
	)
	gt.NoError(t, f.run(t, agentMentionTS)).Required()
	gt.Equal(t, f.slack.recorded(), []slackSearch{{Query: "plan", Count: 5}})
	gt.String(t, f.llm.Inputs()[1].ToolResults[0].Content).Equal(
		`[{"channel_id":"D1","channel_name":"U9","channel_type":"im","user_id":"U9","username":"bob","ts":"1.1","text":"secret","permalink":"https://example.slack.com/p1"}]`)
}

func TestAgent_SessionStates(t *testing.T) {
	begin := func(t *testing.T, f *agentFixture, key model.UserKey) {
		t.Helper()
		_, err := f.repo.AgentSession().Begin(context.Background(), key, model.AgentSessionBeginRequest{
			ID: sessionID(t), ChannelID: testChannel, ThreadTS: agentThreadTS, LeaseID: "other-run",
			Now: f.now, LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "g",
		})
		gt.NoError(t, err).Required()
	}
	t.Run("busy", func(t *testing.T) {
		f := newAgentFixture(t)
		begin(t, f, testKey)
		gt.NoError(t, f.run(t, agentMentionTS)).Required()
		gt.Equal(t, f.bot.Texts("UpdateProgress"), []string{":hourglass_flowing_sand: Still working on your previous request in this thread"})
		gt.Array(t, f.llm.Configs()).Length(0)
		gt.Array(t, f.bot.Recorded("PostAnswer")).Length(0)
	})
	t.Run("owned by another user", func(t *testing.T) {
		f := newAgentFixture(t)
		begin(t, f, model.UserKey{TeamID: testKey.TeamID, UserID: "U0999ZZZZ"})
		gt.Error(t, f.run(t, agentMentionTS)).Is(usecase.ErrThreadOwnedByOther)
		// The Slack event handler tells the user; the agent touches nothing.
		gt.Array(t, f.bot.Recorded()).Length(0)
		gt.Array(t, f.llm.Configs()).Length(0)
	})
}

func TestAgent_OwnedByOther(t *testing.T) {
	f := newAgentFixture(t)
	a := f.agent(t)
	owned, err := a.OwnedByOther(context.Background(), testKey, testChannel, agentThreadTS)
	gt.NoError(t, err).Required()
	gt.False(t, owned)

	_, err = f.repo.AgentSession().Begin(context.Background(), model.UserKey{TeamID: testKey.TeamID, UserID: "U0999ZZZZ"}, model.AgentSessionBeginRequest{
		ID: sessionID(t), ChannelID: testChannel, ThreadTS: agentThreadTS, LeaseID: "l",
		Now: f.now, LeaseExpiresAt: f.now.Add(time.Minute), TTL: time.Hour, NewGeneration: "g",
	})
	gt.NoError(t, err).Required()
	owned, err = a.OwnedByOther(context.Background(), testKey, testChannel, agentThreadTS)
	gt.NoError(t, err).Required()
	gt.True(t, owned)
}

func TestAgent_ProgressFailures(t *testing.T) {
	t.Run("update fails", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.UpdateErr = errors.New("ratelimited")
		f.llm.Script(
			toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
			textTurn("answer", model.LLMUsage{}),
		)
		gt.NoError(t, f.run(t, agentMentionTS)).Required()
		gt.Array(t, f.bot.Recorded("UpdateProgress")).Length(2)
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"answer"})
		gt.Array(t, f.storedMessages(t)).Length(4)
	})
	t.Run("progress message deleted", func(t *testing.T) {
		f := newAgentFixture(t)
		f.bot.UpdateErr = goerr.Wrap(interfaces.ErrSlackMessageNotFound, "message_not_found")
		f.llm.Script(
			toolTurn(model.LLMUsage{}, toolCall("c1", "slack_search_messages", `{"query":"a"}`)),
			toolTurn(model.LLMUsage{}, toolCall("c2", "slack_search_messages", `{"query":"b"}`)),
			textTurn("answer", model.LLMUsage{}),
		)
		gt.NoError(t, f.run(t, agentMentionTS)).Required()
		gt.Array(t, f.bot.Recorded("UpdateProgress")).Length(1)
		gt.Equal(t, f.bot.Texts("PostAnswer"), []string{"answer"})
		gt.Array(t, f.storedMessages(t)).Length(6)
	})
}

func TestNew_InvalidConfig(t *testing.T) {
	cases := map[string]func(c *mention.Config){
		"no base URL":       func(c *mention.Config) { c.BaseURL = "" },
		"zero budget":       func(c *mention.Config) { c.Budget = 0 },
		"ratio of one":      func(c *mention.Config) { c.NoticeRatio = 1 },
		"one model call":    func(c *mention.Config) { c.MaxLLMCalls = 1 },
		"no tool calls":     func(c *mention.Config) { c.MaxToolCalls = 0 },
		"no timeout":        func(c *mention.Config) { c.Timeout = 0 },
		"no session TTL":    func(c *mention.Config) { c.SessionTTL = 0 },
		"no result limit":   func(c *mention.Config) { c.ToolResultLimit = 0 },
		"no history limit":  func(c *mention.Config) { c.HistoryByteLimit = 0 },
		"zero output price": func(c *mention.Config) { c.Rate.Output = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			mutate(&cfg)
			_, err := mention.New(memory.New(), &usecasetest.LLM{}, usecasetest.NewSlackBot(), mention.Services{}, cfg)
			gt.Error(t, err)
		})
	}
}
