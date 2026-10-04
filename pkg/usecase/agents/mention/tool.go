package mention

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
)

// agentTool is one read-only tool the model can call.
type agentTool struct {
	spec model.LLMToolSpec
	// service names the integration in messages to the model.
	service string
	// describe writes one progress line for a call.
	describe func(input json.RawMessage) string
	// run returns the JSON text given to the model.
	run func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error)
}

// agentToolInputError carries a message written by Robin that is safe to
// show the model.
type agentToolInputError struct{ msg string }

func (e *agentToolInputError) Error() string { return e.msg }

func inputError(format string, args ...any) error {
	return &agentToolInputError{msg: fmt.Sprintf(format, args...)}
}

// decodeInput parses the tool input. The JSON error says where the input is
// wrong and contains no data other than the input itself.
func decodeInput[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, inputError("Invalid input: %s", err.Error())
	}
	return v, nil
}

func requireString(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return inputError("Invalid input: %s is required.", name)
	}
	return nil
}

// intInRange returns def for 0 (omitted) and checks the range otherwise.
func intInRange(name string, value, def, minValue, maxValue int) (int, error) {
	if value == 0 {
		return def, nil
	}
	if value < minValue || value > maxValue {
		return 0, inputError("Invalid input: %s must be between %d and %d.", name, minValue, maxValue)
	}
	return value, nil
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", goerr.Wrap(err, "failed to encode tool result")
	}
	return string(b), nil
}

func quoted(s string) string {
	return "“" + truncateText(s, progressQueryChars) + "”"
}

// truncateResult keeps the first limit characters of a tool result.
func truncateResult(s string, limit int) string {
	n := utf8.RuneCountInString(s)
	if n <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + fmt.Sprintf("\n[truncated: %d more characters]", n-limit)
}

// toolErrorText turns a tool error into a message for the model. Errors that
// are not classified are recorded and reported as an internal error, so no
// error text from Robin or an external service reaches the model.
func toolErrorText(ctx context.Context, tool *agentTool, settingsURL string, err error) string {
	var inErr *agentToolInputError
	switch {
	case errors.As(err, &inErr):
		return inErr.msg
	case errors.Is(err, usecase.ErrNotionNotConnected), errors.Is(err, usecase.ErrGoogleWorkspaceNotConnected),
		errors.Is(err, usecase.ErrGitHubNotConnected):
		return fmt.Sprintf("%s is not connected. The user can connect it at %s.", tool.service, settingsURL)
	case errors.Is(err, usecase.ErrNotionReconnectRequired), errors.Is(err, usecase.ErrGoogleWorkspaceReconnectRequired),
		errors.Is(err, interfaces.ErrGitHubTokenInvalid), errors.Is(err, interfaces.ErrSlackTokenInvalid):
		return fmt.Sprintf("%s rejected the stored connection. The user has to reconnect it at %s.", tool.service, settingsURL)
	case errors.Is(err, interfaces.ErrNotionNotFound), errors.Is(err, interfaces.ErrNotionForbidden),
		errors.Is(err, interfaces.ErrGoogleNotFound), errors.Is(err, interfaces.ErrGitHubNotFound):
		return "Not found, or not shared with this user."
	case errors.Is(err, interfaces.ErrNotionRateLimited):
		return "Rate limited by Notion. Try again later."
	case errors.Is(err, usecase.ErrNotionInvalidRequest):
		return "Invalid request."
	case errors.Is(err, interfaces.ErrGoogleUnsupportedFile):
		return "This file type cannot be read as text."
	default:
		errutil.Handle(ctx, goerr.Wrap(err, "agent tool failed", goerr.V("tool", tool.spec.Name)), "agent tool failed")
		return "Internal error."
	}
}

// modelToolSpec builds a tool definition from a schema literal. The schemas
// are constants of this package, so a broken one is a programming error.
func modelToolSpec(name, description, inputSchema string) model.LLMToolSpec {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(inputSchema)); err != nil {
		panic("invalid input schema of tool " + name + ": " + err.Error())
	}
	return model.LLMToolSpec{Name: name, Description: description, InputSchema: buf.Bytes()}
}

// buildAgentTools lists the tools of the integrations enabled on this server.
// The list is the same for every user and run: the model treats its earlier
// reasoning as invalid when the tools change.
func buildAgentTools(services Services) []*agentTool {
	tools := slackTools()
	if services.Notion != nil {
		tools = append(tools, notionTools(services.Notion)...)
	}
	if services.Google != nil {
		tools = append(tools, googleTools(services.Google)...)
	}
	if services.GitHub != nil {
		tools = append(tools, githubTools(services.GitHub)...)
	}
	return tools
}
