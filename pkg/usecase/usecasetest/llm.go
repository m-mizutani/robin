package usecasetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// LLMStep is one scripted answer of the fake model.
type LLMStep struct {
	Turn *model.LLMTurn
	Err  error
	// Wait blocks until the context ends and returns its error.
	Wait bool
}

// LLM answers Send with the scripted steps in order and records every
// session it started and every input. Each successful Send appends two
// history messages of format "fake.v1": "in-N" and "out-N".
type LLM struct {
	mu            sync.Mutex
	steps         []LLMStep
	NewSessionErr error
	configs       []model.LLMSessionConfig
	histories     [][]model.LLMHistoryMessage
	inputs        []model.LLMInput
	sent          int
}

var _ interfaces.LLMClient = &LLM{}

func (f *LLM) Script(steps ...LLMStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, steps...)
}

func (f *LLM) NewSession(cfg model.LLMSessionConfig, history []model.LLMHistoryMessage) (interfaces.LLMSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs = append(f.configs, cfg)
	f.histories = append(f.histories, append([]model.LLMHistoryMessage(nil), history...))
	if f.NewSessionErr != nil {
		return nil, f.NewSessionErr
	}
	return &llmSession{llm: f}, nil
}

// Configs returns the configuration of every session started.
func (f *LLM) Configs() []model.LLMSessionConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.LLMSessionConfig(nil), f.configs...)
}

// Histories returns the history every session started with.
func (f *LLM) Histories() [][]model.LLMHistoryMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]model.LLMHistoryMessage(nil), f.histories...)
}

// Inputs returns every input sent, in order.
func (f *LLM) Inputs() []model.LLMInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.LLMInput(nil), f.inputs...)
}

type llmSession struct {
	llm      *LLM
	appended []model.LLMHistoryMessage
}

func (s *llmSession) Send(ctx context.Context, in model.LLMInput) (*model.LLMTurn, error) {
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

	if step.Wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if step.Err != nil {
		return nil, step.Err
	}
	s.appended = append(s.appended,
		model.LLMHistoryMessage{Format: "fake.v1", Data: []byte(fmt.Sprintf("in-%d", n))},
		model.LLMHistoryMessage{Format: "fake.v1", Data: []byte(fmt.Sprintf("out-%d", n))})
	return step.Turn, nil
}

func (s *llmSession) Appended() []model.LLMHistoryMessage {
	return append([]model.LLMHistoryMessage(nil), s.appended...)
}

// TextTurn is a final answer.
func TextTurn(text string, usage model.LLMUsage) LLMStep {
	return LLMStep{Turn: &model.LLMTurn{
		Blocks:     []model.LLMOutputBlock{{Kind: model.LLMOutputText, Text: text}},
		StopReason: model.LLMStopEndTurn,
		Usage:      usage,
	}}
}

// ToolTurn is a response that calls tools.
func ToolTurn(usage model.LLMUsage, blocks ...model.LLMOutputBlock) LLMStep {
	return LLMStep{Turn: &model.LLMTurn{Blocks: blocks, StopReason: model.LLMStopToolUse, Usage: usage}}
}

func ToolCall(id, name, input string) model.LLMOutputBlock {
	return model.LLMOutputBlock{Kind: model.LLMOutputToolCall, ToolCall: &model.LLMToolCall{ID: id, Name: name, Input: json.RawMessage(input)}}
}
