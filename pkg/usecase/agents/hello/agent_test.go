package hello_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/usecase/agents/hello"
	"github.com/m-mizutani/robin/pkg/usecase/usecasetest"
)

var rate = model.Rate{Input: 2000, Output: 10000, CacheRead: 200, CacheWrite: 2500}

func runRequest() usecase.JobRunRequest {
	return usecase.JobRunRequest{
		Key: model.UserKey{TeamID: "T0123", UserID: "U0ALICE"},
		Job: model.Job{
			TeamID:      "T0123",
			UserID:      "U0ALICE",
			ID:          "00000000-0000-4000-8000-000000000001",
			Kind:        model.JobKindHello,
			ChannelID:   "C0GENERAL",
			ChannelName: "general",
			Schedule:    model.DailySchedule{Hour: 9, Minute: 0, TimeZone: "Asia/Tokyo"},
		},
		RunID: "20261005T000000Z",
		// Monday 09:00 in Tokyo, still Sunday in UTC.
		ScheduledAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
	}
}

func newAgent(t *testing.T, llm *usecasetest.LLM, bot *usecasetest.SlackBot) *hello.Agent {
	t.Helper()
	a, err := hello.New(llm, bot, hello.Config{Rate: rate})
	gt.NoError(t, err).Required()
	return a
}

var usage = model.LLMUsage{InputTokens: 300, OutputTokens: 40}

func TestAgent_PostsTheGreeting(t *testing.T) {
	llm := &usecasetest.LLM{}
	llm.Script(usecasetest.TextTurn("  Good morning, everyone! Happy Monday.\n", usage))
	bot := usecasetest.NewSlackBot()

	res, err := newAgent(t, llm, bot).Run(context.Background(), runRequest())
	gt.NoError(t, err).Required()
	gt.Equal(t, res, &usecase.JobRunResult{MessageTS: "1900000000.000001", Spent: rate.Cost(usage)})

	configs := llm.Configs()
	gt.A(t, configs).Length(1).Required()
	gt.String(t, configs[0].SystemPrompt).Equal(hello.SystemPromptForTest)
	gt.A(t, configs[0].Tools).Length(0)
	gt.A(t, llm.Histories()[0]).Length(0)

	inputs := llm.Inputs()
	gt.A(t, inputs).Length(1).Required()
	gt.String(t, inputs[0].UserText).Equal("Date: 2026-10-05 (Monday)\nTime zone: Asia/Tokyo\nChannel: #general\nWrite the greeting.")

	gt.Equal(t, bot.Recorded(), []usecasetest.SlackCall{{
		Method:    "PostMessage",
		ChannelID: "C0GENERAL",
		Requester: "U0ALICE",
		TS:        "1900000000.000001",
		Text:      "Good morning, everyone! Happy Monday.",
	}})
}

func TestAgent_ModelFailure(t *testing.T) {
	llmErr := errors.New("overloaded")
	llm := &usecasetest.LLM{}
	llm.Script(usecasetest.LLMStep{Err: llmErr})
	bot := usecasetest.NewSlackBot()

	res, err := newAgent(t, llm, bot).Run(context.Background(), runRequest())
	gt.Error(t, err).Is(llmErr)
	gt.Value(t, res).Nil()
	gt.A(t, bot.Recorded()).Length(0)
}

func TestAgent_NoGreeting(t *testing.T) {
	cases := map[string]*model.LLMTurn{
		"refusal": {Blocks: []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: "I can't."}},
			StopReason: model.LLMStopRefusal, Usage: usage},
		"empty text": {Blocks: []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: "  "}},
			StopReason: model.LLMStopEndTurn, Usage: usage},
		"cut at the token limit": {Blocks: []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: "Good mor"}},
			StopReason: model.LLMStopMaxTokens, Usage: usage},
	}
	for name, turn := range cases {
		t.Run(name, func(t *testing.T) {
			llm := &usecasetest.LLM{}
			llm.Script(usecasetest.LLMStep{Turn: turn})
			bot := usecasetest.NewSlackBot()

			res, err := newAgent(t, llm, bot).Run(context.Background(), runRequest())
			gt.Error(t, err).Is(hello.ErrNoGreeting)
			gt.Equal(t, res, &usecase.JobRunResult{Spent: rate.Cost(usage)})
			gt.A(t, bot.Recorded()).Length(0)
		})
	}
}

func TestAgent_PostFailure(t *testing.T) {
	llm := &usecasetest.LLM{}
	llm.Script(usecasetest.TextTurn("Good morning!", usage))
	bot := usecasetest.NewSlackBot()
	bot.MessageErr = errors.New("not_in_channel")

	res, err := newAgent(t, llm, bot).Run(context.Background(), runRequest())
	gt.Error(t, err).Is(bot.MessageErr)
	gt.Equal(t, res, &usecase.JobRunResult{Spent: rate.Cost(usage)})
}

func TestAgent_UnknownTimeZone(t *testing.T) {
	llm := &usecasetest.LLM{}
	bot := usecasetest.NewSlackBot()
	req := runRequest()
	req.Job.Schedule.TimeZone = "Mars/Base"

	_, err := newAgent(t, llm, bot).Run(context.Background(), req)
	gt.Value(t, err).NotNil()
	gt.A(t, llm.Configs()).Length(0)
	gt.A(t, bot.Recorded()).Length(0)
}

func TestNew_RejectsInvalidRate(t *testing.T) {
	_, err := hello.New(&usecasetest.LLM{}, usecasetest.NewSlackBot(), hello.Config{})
	gt.Value(t, err).NotNil()
}
