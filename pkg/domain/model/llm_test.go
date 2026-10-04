package model_test

import (
	"encoding/json"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

func TestLLMToolSpec_Validate(t *testing.T) {
	valid := model.LLMToolSpec{Name: "notion_search", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}
	gt.NoError(t, valid.Validate())

	cases := map[string]model.LLMToolSpec{
		"upper case name":   {Name: "Notion", Description: "d", InputSchema: valid.InputSchema},
		"empty description": {Name: "x", InputSchema: valid.InputSchema},
		"array schema":      {Name: "x", Description: "d", InputSchema: json.RawMessage(`{"type":"array"}`)},
		"broken schema":     {Name: "x", Description: "d", InputSchema: json.RawMessage(`{`)},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			gt.Error(t, spec.Validate())
		})
	}
}

func TestLLMInput_Validate(t *testing.T) {
	gt.NoError(t, model.LLMInput{UserText: "hi"}.Validate())
	gt.NoError(t, model.LLMInput{ToolResults: []model.LLMToolResult{{CallID: "c"}}}.Validate())
	gt.Error(t, model.LLMInput{}.Validate())
	gt.Error(t, model.LLMInput{ToolResults: []model.LLMToolResult{{}}}.Validate())
}

func TestLLMTurn(t *testing.T) {
	call := &model.LLMToolCall{ID: "c1", Name: "x"}
	turn := &model.LLMTurn{Blocks: []model.LLMOutputBlock{
		{Kind: model.LLMOutputProgress, Text: "thinking"},
		{Kind: model.LLMOutputText, Text: "a"},
		{Kind: model.LLMOutputToolCall, ToolCall: call},
		{Kind: model.LLMOutputText, Text: "b"},
	}}
	gt.Equal(t, turn.Text(), "a\nb")
	gt.Equal(t, turn.ToolCalls(), []model.LLMToolCall{*call})
}
