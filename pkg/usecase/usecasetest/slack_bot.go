// Package usecasetest holds test doubles of domain interfaces shared by the
// tests of the usecase packages (pkg/usecase and the agents under
// pkg/usecase/agents). It is imported only by tests.
package usecasetest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// SlackCall is one Slack call the bot token would make. Method is the
// SlackBot method name.
type SlackCall struct {
	Method    string
	ChannelID string
	UserID    model.SlackUserID // the receiver of an ephemeral message
	Requester model.SlackUserID
	ThreadTS  string
	TS        string
	Text      string
}

// SlackMessage is the part of a posted message that tests compare.
type SlackMessage struct {
	ChannelID string
	UserID    model.SlackUserID
	ThreadTS  string
	Text      string
}

// SlackBot records every call in order. The Err fields make the method of
// the same name fail; Thread and Message are what the reads return.
type SlackBot struct {
	mu           sync.Mutex
	Names        map[model.SlackUserID]string
	NameErr      error
	EphemeralErr error
	PostErr      error // PostProgress
	UpdateErr    error // UpdateProgress
	AnswerErr    error // PostAnswer
	ThreadErr    error // GetThreadMessages
	GetErr       error // GetMessage
	DeleteErr    error // DeleteMessage
	Thread       []model.SlackThreadMessage
	Message      *model.SlackPostedMessage
	nextTS       int
	calls        []SlackCall
}

var _ interfaces.SlackBot = &SlackBot{}

func NewSlackBot() *SlackBot {
	return &SlackBot{Names: make(map[model.SlackUserID]string)}
}

func (b *SlackBot) record(c SlackCall) {
	b.calls = append(b.calls, c)
}

func (b *SlackBot) PostEphemeral(_ context.Context, channelID string, userID model.SlackUserID, threadTS, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(SlackCall{Method: "PostEphemeral", ChannelID: channelID, UserID: userID, ThreadTS: threadTS, Text: text})
	return b.EphemeralErr
}

func (b *SlackBot) GetUserName(_ context.Context, userID model.SlackUserID) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.NameErr != nil {
		return "", b.NameErr
	}
	return b.Names[userID], nil
}

// PostProgress returns ts "1800000000.000001", "...002" and so on.
func (b *SlackBot) PostProgress(_ context.Context, channelID, threadTS string, requester model.SlackUserID, text string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextTS++
	ts := fmt.Sprintf("1800000000.%06d", b.nextTS)
	b.record(SlackCall{Method: "PostProgress", ChannelID: channelID, ThreadTS: threadTS, Requester: requester, TS: ts, Text: text})
	if b.PostErr != nil {
		return "", b.PostErr
	}
	return ts, nil
}

func (b *SlackBot) UpdateProgress(_ context.Context, channelID, messageTS string, requester model.SlackUserID, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(SlackCall{Method: "UpdateProgress", ChannelID: channelID, TS: messageTS, Requester: requester, Text: text})
	return b.UpdateErr
}

func (b *SlackBot) PostAnswer(_ context.Context, channelID, threadTS string, requester model.SlackUserID, markdown string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(SlackCall{Method: "PostAnswer", ChannelID: channelID, ThreadTS: threadTS, Requester: requester, Text: markdown})
	return b.AnswerErr
}

// GetThreadMessages records its arguments in Text as
// "after={afterTS} before={beforeTS} limit={limit}".
func (b *SlackBot) GetThreadMessages(_ context.Context, channelID, threadTS, afterTS, beforeTS string, limit int) ([]model.SlackThreadMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(SlackCall{Method: "GetThreadMessages", ChannelID: channelID, ThreadTS: threadTS,
		Text: fmt.Sprintf("after=%s before=%s limit=%d", afterTS, beforeTS, limit)})
	if b.ThreadErr != nil {
		return nil, b.ThreadErr
	}
	return append([]model.SlackThreadMessage(nil), b.Thread...), nil
}

func (b *SlackBot) GetMessage(_ context.Context, channelID, ts string) (*model.SlackPostedMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(SlackCall{Method: "GetMessage", ChannelID: channelID, TS: ts})
	if b.GetErr != nil {
		return nil, b.GetErr
	}
	msg := *b.Message
	return &msg, nil
}

func (b *SlackBot) DeleteMessage(_ context.Context, channelID, ts string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(SlackCall{Method: "DeleteMessage", ChannelID: channelID, TS: ts})
	return b.DeleteErr
}

// Recorded returns the calls of the given methods, or every call.
func (b *SlackBot) Recorded(methods ...string) []SlackCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []SlackCall
	for _, c := range b.calls {
		if len(methods) == 0 || slices.Contains(methods, c.Method) {
			out = append(out, c)
		}
	}
	return out
}

// Methods returns the method names of every call, in order.
func (b *SlackBot) Methods() []string {
	var out []string
	for _, c := range b.Recorded() {
		out = append(out, c.Method)
	}
	return out
}

// Texts returns the Text of every call of method.
func (b *SlackBot) Texts(method string) []string {
	var out []string
	for _, c := range b.Recorded(method) {
		out = append(out, c.Text)
	}
	return out
}

func (b *SlackBot) Answers() []SlackMessage {
	var out []SlackMessage
	for _, c := range b.Recorded("PostAnswer") {
		out = append(out, SlackMessage{ChannelID: c.ChannelID, ThreadTS: c.ThreadTS, Text: c.Text})
	}
	return out
}

func (b *SlackBot) EphemeralMessages() []SlackMessage {
	var out []SlackMessage
	for _, c := range b.Recorded("PostEphemeral") {
		out = append(out, SlackMessage{ChannelID: c.ChannelID, UserID: c.UserID, ThreadTS: c.ThreadTS, Text: c.Text})
	}
	return out
}
