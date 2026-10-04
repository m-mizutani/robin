// Package mention is the agent that answers a Slack mention in its thread
// with a model that reads the requester's connected services.
package mention

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
	"github.com/m-mizutani/robin/pkg/utils/logging"
)

// slackTimeout bounds each Slack call and lease release made after the
// run's own deadline, so the reply still reaches the thread after a timeout.
const slackTimeout = 30 * time.Second

// ErrSessionLeaseLost means another run took the session's lease before this
// run committed.
var ErrSessionLeaseLost = errors.New("agent session lease was lost")

type Config struct {
	BaseURL         string // for the settings URL
	Rate            model.Rate
	Budget          model.NanoUSD
	NoticeRatio     float64 // 0 < x < 1
	MaxLLMCalls     int     // >= 2
	MaxToolCalls    int     // per step
	Timeout         time.Duration
	LeaseMargin     time.Duration // lease = Timeout + LeaseMargin
	SessionTTL      time.Duration
	ToolResultLimit int // characters
	ThreadLimit     int // messages
	ThreadCharLimit int // characters
	// HistoryByteLimit makes a run conclude once the messages it would store
	// reach this size, so that one commit stays within a Firestore request.
	HistoryByteLimit int
}

func (c Config) Validate() error {
	if c.BaseURL == "" {
		return goerr.New("empty agent base URL")
	}
	if err := c.Rate.Validate(); err != nil {
		return goerr.Wrap(err, "invalid agent rate")
	}
	switch {
	case c.Budget <= 0:
		return goerr.New("agent budget must be positive", goerr.V("budget", c.Budget))
	case c.NoticeRatio <= 0 || c.NoticeRatio >= 1:
		return goerr.New("agent notice ratio must be between 0 and 1", goerr.V("ratio", c.NoticeRatio))
	case c.MaxLLMCalls < 2:
		return goerr.New("agent needs at least two model calls", goerr.V("max_llm_calls", c.MaxLLMCalls))
	case c.MaxToolCalls < 1:
		return goerr.New("agent needs at least one tool call per step", goerr.V("max_tool_calls", c.MaxToolCalls))
	case c.Timeout <= 0 || c.LeaseMargin < 0 || c.SessionTTL <= 0:
		return goerr.New("invalid agent durations",
			goerr.V("timeout", c.Timeout), goerr.V("lease_margin", c.LeaseMargin), goerr.V("session_ttl", c.SessionTTL))
	case c.ToolResultLimit <= 0 || c.ThreadLimit <= 0 || c.ThreadCharLimit <= 0 || c.HistoryByteLimit <= 0:
		return goerr.New("invalid agent limits", goerr.V("tool_result_limit", c.ToolResultLimit),
			goerr.V("thread_limit", c.ThreadLimit), goerr.V("thread_char_limit", c.ThreadCharLimit),
			goerr.V("history_byte_limit", c.HistoryByteLimit))
	}
	return nil
}

func (c Config) settingsURL() string {
	return c.BaseURL + "/settings"
}

func (s Services) names() []string {
	names := []string{"Slack messages"}
	if s.Notion != nil {
		names = append(names, "Notion")
	}
	if s.Google != nil {
		names = append(names, "Google Workspace (Gmail, Drive and Calendar)")
	}
	if s.GitHub != nil {
		names = append(names, "GitHub")
	}
	return names
}

// Agent answers one Slack mention with a model that reads the requester's
// connected services.
type Agent struct {
	repo         interfaces.Repository
	llm          interfaces.LLMClient
	bot          interfaces.SlackBot
	services     Services
	cfg          Config
	tools        []*agentTool
	toolByName   map[string]*agentTool
	toolSpecs    []model.LLMToolSpec
	systemPrompt string
	now          func() time.Time
	newID        func() string
}

var _ usecase.MentionAgent = &Agent{}

func New(repo interfaces.Repository, llm interfaces.LLMClient, bot interfaces.SlackBot,
	services Services, cfg Config) (*Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	prompt, err := renderSystemPrompt(services.names(), cfg.settingsURL())
	if err != nil {
		return nil, err
	}
	a := &Agent{
		repo:         repo,
		llm:          llm,
		bot:          bot,
		services:     services,
		cfg:          cfg,
		tools:        buildAgentTools(services),
		toolByName:   make(map[string]*agentTool),
		systemPrompt: prompt,
		now:          time.Now,
		newID:        uuid.NewString,
	}
	for _, t := range a.tools {
		if err := t.spec.Validate(); err != nil {
			return nil, goerr.Wrap(err, "invalid agent tool")
		}
		a.toolByName[t.spec.Name] = t
		a.toolSpecs = append(a.toolSpecs, t.spec)
	}
	return a, nil
}

// OwnedByOther reports whether another user owns the conversation of the thread.
func (a *Agent) OwnedByOther(ctx context.Context, key model.UserKey, channelID, threadTS string) (bool, error) {
	id, err := model.NewAgentSessionID(key.TeamID, channelID, threadTS)
	if err != nil {
		return false, err
	}
	owned, err := a.repo.AgentSession().OwnedByOther(ctx, key, id, a.now())
	if err != nil {
		return false, goerr.Wrap(err, "failed to check the owner of the thread")
	}
	return owned, nil
}

// Run answers the mention; see usecase.MentionAgent.
func (a *Agent) Run(ctx context.Context, req usecase.MentionRequest) error {
	r := &agentRun{
		agent: a,
		req:   req,
		meter: &budgetMeter{
			rate:        a.cfg.Rate,
			limit:       a.cfg.Budget,
			noticeRatio: a.cfg.NoticeRatio,
			maxCalls:    a.cfg.MaxLLMCalls,
		},
	}
	return r.run(ctx)
}

// agentRun is the state of one run. It lives only in the goroutine of the
// run.
type agentRun struct {
	agent        *Agent
	req          usecase.MentionRequest
	meter        *budgetMeter
	toolCalls    int
	sessionID    model.AgentSessionID
	progressGone bool
	ending       context.Context
	cancelEnding context.CancelFunc
}

type runResult string

const (
	resultDone      runResult = "done"
	resultNotSaved  runResult = "not_saved"
	resultNoAnswer  runResult = "no_answer"
	resultCostLimit runResult = "cost_limit"
	resultDeclined  runResult = "declined"
	resultFailed    runResult = "failed"
	resultTimedOut  runResult = "timed_out"
	resultBusy      runResult = "busy"
	resultNotOwner  runResult = "not_owner"
	resultIncompat  runResult = "incompatible_history"
)

func (r *agentRun) vals() []goerr.Option {
	return []goerr.Option{
		goerr.V("team_id", r.req.Key.TeamID), goerr.V("user_id", r.req.Key.UserID),
		goerr.V("channel_id", r.req.ChannelID), goerr.V("thread_ts", r.req.ThreadTS),
		goerr.V("session_id", r.sessionID),
	}
}

// endContext returns the context of the calls that end the run: the last
// progress update, the reply, the commit and the lease release. It outlives
// the run's deadline so a timed out run can still report it, and all of those
// calls share one slackTimeout, which is shorter than the lease margin.
func (r *agentRun) endContext(ctx context.Context) context.Context {
	if r.ending == nil {
		r.ending, r.cancelEnding = context.WithTimeout(context.WithoutCancel(ctx), slackTimeout)
	}
	return r.ending
}

func (r *agentRun) closeEnding() {
	if r.cancelEnding != nil {
		r.cancelEnding()
	}
}

// progress replaces the text of the progress message within ctx: the run's
// context while the run works, endContext when it ends. A failure does not
// stop the run. Once the message is gone (deleted by the requester), no more
// updates are sent.
func (r *agentRun) progress(ctx context.Context, text string) {
	if r.progressGone || ctx.Err() != nil {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, slackTimeout)
	defer cancel()
	err := r.agent.bot.UpdateProgress(sctx, r.req.ChannelID, r.req.ProgressTS, r.req.Key.UserID, text)
	switch {
	case err == nil:
	case errors.Is(err, interfaces.ErrSlackMessageNotFound):
		r.progressGone = true
		errutil.Handle(ctx, goerr.Wrap(err, "progress message was deleted",
			append(r.vals(), goerr.V("ts", r.req.ProgressTS), goerr.T(errutil.TagBenign))...), "progress updates stopped")
	default:
		errutil.Handle(ctx, goerr.Wrap(err, "failed to update progress message", r.vals()...), "progress update failed")
	}
}

// reply posts a message of Robin in the thread when the run ends without an
// answer. A failure is recorded: the run is ending anyway.
func (r *agentRun) reply(ctx context.Context, text string) {
	ectx := r.endContext(ctx)
	if err := r.agent.bot.PostAnswer(ectx, r.req.ChannelID, r.req.ThreadTS, r.req.Key.UserID, text); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to post reply", r.vals()...), "reply was not posted")
	}
}

// end posts the reply when there is one, shows the final progress and
// records the result.
func (r *agentRun) end(ctx context.Context, result runResult, replyText, progressText string) {
	if replyText != "" {
		r.reply(ctx, replyText)
	}
	r.progress(r.endContext(ctx), progressText)
	r.finish(ctx, result)
}

func (r *agentRun) stats() string {
	return runStats(r.meter.calls, r.toolCalls, r.meter.spent)
}

func (r *agentRun) finish(ctx context.Context, result runResult) {
	u := r.meter.usage
	logging.From(ctx).Info("agent run finished",
		slog.String("result", string(result)),
		slog.String("session_id", string(r.sessionID)),
		slog.Int("llm_calls", r.meter.calls),
		slog.Int("tool_calls", r.toolCalls),
		slog.Int64("spent_nano_usd", int64(r.meter.spent)),
		slog.Int64("input_tokens", u.InputTokens),
		slog.Int64("output_tokens", u.OutputTokens),
		slog.Int64("cache_read_input_tokens", u.CacheReadInputTokens),
		slog.Int64("cache_creation_input_tokens", u.CacheCreationInputTokens),
	)
}

func (r *agentRun) fail(ctx context.Context, err error) error {
	r.end(ctx, resultFailed, failedText, progressFailed(r.stats()))
	return goerr.Wrap(err, "agent run failed", r.vals()...)
}

func (r *agentRun) costLimit(ctx context.Context) error {
	r.end(ctx, resultCostLimit, costLimitText(r.agent.cfg.Budget), progressCostLimit(r.agent.cfg.Budget, r.stats()))
	return nil
}

func (r *agentRun) noAnswer(ctx context.Context) error {
	r.end(ctx, resultNoAnswer, noAnswerText, progressNoAnswer(r.stats()))
	return nil
}

func (r *agentRun) declined(ctx context.Context) error {
	r.end(ctx, resultDeclined, declinedText, progressDeclined(r.stats()))
	return nil
}

func (r *agentRun) timedOut(ctx context.Context, err error) error {
	r.end(ctx, resultTimedOut, timedOutText(r.agent.cfg.Timeout), progressTimedOut(r.agent.cfg.Timeout, r.stats()))
	return goerr.Wrap(err, "agent run timed out", r.vals()...)
}

func (r *agentRun) run(ctx context.Context) error {
	a := r.agent
	runCtx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	defer r.closeEnding()

	id, err := model.NewAgentSessionID(r.req.Key.TeamID, r.req.ChannelID, r.req.ThreadTS)
	if err != nil {
		return r.fail(ctx, err)
	}
	r.sessionID = id

	leaseID := a.newID()
	now := a.now()
	begun, err := a.repo.AgentSession().Begin(runCtx, r.req.Key, model.AgentSessionBeginRequest{
		ID:             id,
		ChannelID:      r.req.ChannelID,
		ThreadTS:       r.req.ThreadTS,
		LeaseID:        leaseID,
		Now:            now,
		LeaseExpiresAt: now.Add(a.cfg.Timeout + a.cfg.LeaseMargin),
		TTL:            a.cfg.SessionTTL,
		NewGeneration:  a.newID(),
	})
	if err != nil {
		return r.fail(ctx, goerr.Wrap(err, "failed to begin agent session"))
	}

	switch begun.Status {
	case model.AgentSessionOwnedByOther:
		// The Slack event handler tells the user, as it does when it finds
		// the owner before the run starts.
		r.finish(ctx, resultNotOwner)
		return goerr.Wrap(usecase.ErrThreadOwnedByOther, "another user started the conversation of the thread", r.vals()...)
	case model.AgentSessionBusy:
		r.end(ctx, resultBusy, "", progressBusy)
		return nil
	}

	session := begun.Session
	committed := false
	defer func() {
		if committed {
			return
		}
		if err := a.repo.AgentSession().Release(r.endContext(ctx), r.req.Key, id, leaseID); err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "failed to release agent session lease", r.vals()...),
				"agent session lease expires by itself")
		}
	}()

	resumed := begun.Status == model.AgentSessionResumed
	var history []model.LLMHistoryMessage
	if resumed {
		msgs, err := a.repo.AgentSession().ListMessages(runCtx, r.req.Key, id, session.Generation)
		if err != nil {
			return r.fail(ctx, goerr.Wrap(err, "failed to load agent session messages"))
		}
		if len(msgs) != session.MessageCount {
			return r.fail(ctx, goerr.New("stored message count does not match the session",
				goerr.V("stored", len(msgs)), goerr.V("expected", session.MessageCount)))
		}
		for _, m := range msgs {
			history = append(history, model.LLMHistoryMessage{Format: m.Format, Data: m.Data})
		}
	}

	var thread []model.SlackThreadMessage
	if resumed || r.req.InThread {
		after := ""
		if resumed {
			after = session.LastMentionTS
		}
		thread, err = a.bot.GetThreadMessages(runCtx, r.req.ChannelID, r.req.ThreadTS, after, r.req.MentionTS, a.cfg.ThreadLimit)
		if err != nil {
			return r.fail(ctx, goerr.Wrap(err, "failed to read the thread"))
		}
		if resumed {
			// Robin's earlier answers are already in the conversation.
			kept := thread[:0]
			for _, m := range thread {
				if !m.FromRobin {
					kept = append(kept, m)
				}
			}
			thread = kept
		}
	}

	connections, err := r.connections(runCtx)
	if err != nil {
		return r.fail(ctx, err)
	}

	llm, err := a.llm.NewSession(model.LLMSessionConfig{SystemPrompt: a.systemPrompt, Tools: a.toolSpecs}, history)
	if errors.Is(err, interfaces.ErrLLMHistoryIncompatible) {
		r.end(ctx, resultIncompat, incompatibleText, progressIncompatible)
		return nil
	}
	if err != nil {
		return r.fail(ctx, goerr.Wrap(err, "failed to restore the conversation"))
	}

	input := model.LLMInput{UserText: renderRequest(thread, connections, r.req.Key.UserID,
		r.req.MentionTS, r.req.Text, a.cfg.ThreadCharLimit, a.now())}

	for {
		if r.meter.verdict() == budgetExhausted {
			return r.costLimit(ctx)
		}
		reason := r.meter.reason()
		if reason == concludeNone && historyBytes(llm.Appended()) >= a.cfg.HistoryByteLimit {
			reason = concludeHistory
		}
		concluding := reason != concludeNone
		input.DisableTools = concluding
		input.SystemNotice = budgetNotice(r.meter, reason)

		turn, err := llm.Send(runCtx, input)
		if err != nil {
			err = goerr.Wrap(err, "llm call failed", goerr.V("llm_call", r.meter.calls+1))
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return r.timedOut(ctx, err)
			}
			return r.fail(ctx, err)
		}
		r.meter.add(turn.Usage)

		calls := turn.ToolCalls()
		for _, b := range turn.Blocks {
			if b.Kind == model.LLMOutputProgress || (b.Kind == model.LLMOutputText && len(calls) > 0) {
				if b.Text != "" {
					r.progress(runCtx, progressNote(b.Text))
				}
			}
		}

		if turn.StopReason == model.LLMStopRefusal {
			return r.declined(ctx)
		}
		if len(calls) == 0 || concluding {
			answer := turn.Text()
			if answer == "" {
				return r.noAnswer(ctx)
			}
			if turn.StopReason == model.LLMStopMaxTokens {
				answer += "\n\n" + truncatedAnswerNote
			}
			committed, err = r.complete(ctx, llm, leaseID, answer, reason)
			return err
		}
		if r.meter.verdict() == budgetExhausted {
			return r.costLimit(ctx)
		}

		input = model.LLMInput{ToolResults: r.runTools(runCtx, calls)}
	}
}

// historyBytes is the size of the messages a run would store. One commit
// writes them in one Firestore transaction, which is limited to 10 MiB.
func historyBytes(msgs []model.LLMHistoryMessage) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Data) + len(m.Format)
	}
	return n
}

// connections lists the connection state of every enabled integration.
func (r *agentRun) connections(ctx context.Context) ([]connectionLine, error) {
	s := r.agent.services
	key := r.req.Key
	url := r.agent.cfg.settingsURL()
	status := func(connected, reconnect bool) string {
		switch {
		case reconnect:
			return "needs reconnection (reconnect at " + url + ")"
		case connected:
			return "connected"
		default:
			return "not connected (connect at " + url + ")"
		}
	}

	lines := []connectionLine{{service: "Slack", status: "connected"}}
	if s.Notion != nil {
		st, err := s.Notion.Connection(ctx, key)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read the notion connection")
		}
		lines = append(lines, connectionLine{service: "Notion", status: status(st.Connected, st.NeedsReconnect)})
	}
	if s.Google != nil {
		st, err := s.Google.Connection(ctx, key)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read the google workspace connection")
		}
		lines = append(lines, connectionLine{service: "Google Workspace", status: status(st.Connected, false)})
	}
	if s.GitHub != nil {
		st, err := s.GitHub.Connection(ctx, key)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read the github connection")
		}
		lines = append(lines, connectionLine{service: "GitHub", status: status(st.Connected, false)})
	}
	return lines, nil
}

// runTools runs up to MaxToolCalls calls in order and answers the rest with
// an error result. Once the run's deadline passes, no further tool runs.
func (r *agentRun) runTools(ctx context.Context, calls []model.LLMToolCall) []model.LLMToolResult {
	a := r.agent
	results := make([]model.LLMToolResult, 0, len(calls))
	for i, call := range calls {
		if ctx.Err() != nil {
			results = append(results, model.LLMToolResult{CallID: call.ID, IsError: true,
				Content: "not run: the time limit of this request was reached"})
			continue
		}
		if i >= a.cfg.MaxToolCalls {
			results = append(results, model.LLMToolResult{CallID: call.ID, IsError: true,
				Content: "too many tool calls in one step; at most " + plural(a.cfg.MaxToolCalls, "call") + " run"})
			continue
		}
		tool, ok := a.toolByName[call.Name]
		if !ok {
			results = append(results, model.LLMToolResult{CallID: call.ID, IsError: true, Content: "unknown tool: " + call.Name})
			continue
		}
		r.progress(ctx, progressTool(tool.describe(call.Input)))
		r.toolCalls++
		out, err := tool.run(ctx, r.req, call.Input)
		if err != nil {
			results = append(results, model.LLMToolResult{CallID: call.ID, IsError: true,
				Content: toolErrorText(ctx, tool, a.cfg.settingsURL(), err)})
			continue
		}
		results = append(results, model.LLMToolResult{CallID: call.ID, Content: truncateResult(out, a.cfg.ToolResultLimit)})
	}
	return results
}

// complete posts the answer and appends the run to the conversation. It
// reports whether the session was committed.
func (r *agentRun) complete(ctx context.Context, llm interfaces.LLMSession, leaseID, answer string, reason concludeReason) (bool, error) {
	a := r.agent
	ectx := r.endContext(ctx)
	if err := a.bot.PostAnswer(ectx, r.req.ChannelID, r.req.ThreadTS, r.req.Key.UserID, answer); err != nil {
		r.end(ctx, resultFailed, "", progressFailed(r.stats()))
		return false, goerr.Wrap(err, "failed to post the answer", r.vals()...)
	}

	appended := llm.Appended()
	msgs := make([]*model.AgentSessionMessage, 0, len(appended))
	for _, m := range appended {
		msgs = append(msgs, &model.AgentSessionMessage{Format: m.Format, Data: m.Data})
	}
	ok, err := a.repo.AgentSession().Commit(ectx, r.req.Key, r.sessionID, leaseID, model.AgentSessionCommit{
		Messages:      msgs,
		LastMentionTS: r.req.MentionTS,
		Now:           a.now(),
	})
	if err != nil {
		r.end(ctx, resultNotSaved, "", progressNotSaved(r.stats()))
		return false, goerr.Wrap(err, "failed to save the conversation", append(r.vals(), goerr.V("bytes", historyBytes(appended)))...)
	}
	r.end(ctx, resultDone, "", progressDone(r.stats(), reason))
	if !ok {
		// The answer stands; only another run that took the session after
		// the lease expired keeps the conversation.
		return false, goerr.Wrap(ErrSessionLeaseLost, "another run took the session before this run saved it", r.vals()...)
	}
	return true, nil
}
