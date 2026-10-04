package slack_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/m-mizutani/gt"
	slackgo "github.com/slack-go/slack"

	"github.com/m-mizutani/robin/pkg/adapter/slack"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type recordedRequest struct {
	Path string
	Form url.Values
}

// fakeSlack is an httptest server that records each API call and answers with
// the JSON registered for its path.
type fakeSlack struct {
	mu        sync.Mutex
	requests  []recordedRequest
	responses map[string]string
	server    *httptest.Server
}

func newFakeSlack(t *testing.T, responses map[string]string) *fakeSlack {
	t.Helper()
	f := &fakeSlack{responses: responses}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.NoError(t, r.ParseForm())
		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{Path: r.URL.Path, Form: r.PostForm})
		body, ok := f.responses[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			body = `{"ok":false,"error":"unknown_method"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSlack) apiURL() string { return f.server.URL + "/api/" }

func (f *fakeSlack) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

type sentBlock struct {
	Type     string `json:"type"`
	BlockID  string `json:"block_id"`
	Text     string `json:"text"`
	Elements []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"elements"`
}

func decodeBlocks(t *testing.T, form url.Values) []sentBlock {
	t.Helper()
	var blocks []sentBlock
	gt.NoError(t, json.Unmarshal([]byte(form.Get("blocks")), &blocks)).Required()
	return blocks
}

func TestBot_PostProgress(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postMessage": `{"ok":true,"channel":"C0123","ts":"1700000000.000200"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	ts, err := bot.PostProgress(context.Background(), "C0123", "1700000000.000100", "U1", ":thought_balloon: Thinking...")
	gt.NoError(t, err).Required()
	gt.String(t, ts).Equal("1700000000.000200")

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Path).Equal("/api/chat.postMessage")
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")
	gt.String(t, reqs[0].Form.Get("thread_ts")).Equal("1700000000.000100")
	gt.String(t, reqs[0].Form.Get("text")).Equal(":thought_balloon: Thinking...")
	blocks := decodeBlocks(t, reqs[0].Form)
	gt.Array(t, blocks).Length(1).Required()
	gt.String(t, blocks[0].Type).Equal("context")
	gt.String(t, blocks[0].BlockID).Equal("robin_progress:U1")
	gt.Array(t, blocks[0].Elements).Length(1).Required()
	gt.String(t, blocks[0].Elements[0].Type).Equal("mrkdwn")
	gt.String(t, blocks[0].Elements[0].Text).Equal(":thought_balloon: Thinking...")
}

func TestBot_UpdateProgress(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.update": `{"ok":true,"channel":"C0123","ts":"1700000000.000200","text":"x"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	gt.NoError(t, bot.UpdateProgress(context.Background(), "C0123", "1700000000.000200", "U1", ":mag: Searching")).Required()

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Path).Equal("/api/chat.update")
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")
	gt.String(t, reqs[0].Form.Get("ts")).Equal("1700000000.000200")
	blocks := decodeBlocks(t, reqs[0].Form)
	gt.Array(t, blocks).Length(1).Required()
	gt.String(t, blocks[0].Type).Equal("context")
	gt.String(t, blocks[0].BlockID).Equal("robin_progress:U1")
	gt.String(t, blocks[0].Elements[0].Text).Equal(":mag: Searching")
}

func TestBot_UpdateProgressDeleted(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.update": `{"ok":false,"error":"message_not_found"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	err := bot.UpdateProgress(context.Background(), "C0123", "1.1", "U1", "x")
	gt.Error(t, err).Is(interfaces.ErrSlackMessageNotFound)
}

func TestBot_PostAnswer(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postMessage": `{"ok":true,"channel":"C0123","ts":"1700000000.000200"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	gt.NoError(t, bot.PostAnswer(context.Background(), "C0123", "1700000000.000100", "U1", "# Answer\nbody")).Required()
	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Form.Get("thread_ts")).Equal("1700000000.000100")
	gt.String(t, reqs[0].Form.Get("text")).Equal("# Answer\nbody")
	blocks := decodeBlocks(t, reqs[0].Form)
	gt.Array(t, blocks).Length(1).Required()
	gt.String(t, blocks[0].Type).Equal("markdown")
	gt.String(t, blocks[0].BlockID).Equal("robin_answer:U1")
	gt.String(t, blocks[0].Text).Equal("# Answer\nbody")
}

func TestBot_PostAnswerSplits(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postMessage": `{"ok":true,"channel":"C0123","ts":"1700000000.000200"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	// 250 lines of 99 characters and a newline: 25,000 characters.
	line := strings.Repeat("あ", 99) + "\n"
	text := strings.Repeat(line, 250)
	gt.NoError(t, bot.PostAnswer(context.Background(), "C0123", "1.1", "U1", text)).Required()

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(3).Required()
	var joined strings.Builder
	for i, r := range reqs {
		blocks := decodeBlocks(t, r.Form)
		gt.Array(t, blocks).Length(1).Required()
		n := utf8.RuneCountInString(blocks[0].Text)
		gt.Bool(t, n <= 11000).True()
		gt.Bool(t, utf8.RuneCountInString(r.Form.Get("text")) <= 3000).True()
		gt.Bool(t, strings.HasSuffix(blocks[0].Text, "あ")).True()
		if i > 0 {
			joined.WriteString("\n")
		}
		joined.WriteString(blocks[0].Text)
	}
	gt.String(t, joined.String()+"\n").Equal(text)
}

func TestBot_GetThreadMessages(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/conversations.replies": `{"ok":true,"has_more":false,"messages":[
			{"type":"message","user":"U1","text":"parent","ts":"1700000000.000100","thread_ts":"1700000000.000100"},
			{"type":"message","bot_id":"B1","text":"progress","ts":"1700000000.000200","blocks":[{"type":"context","block_id":"robin_progress:U1","elements":[{"type":"mrkdwn","text":"progress"}]}]},
			{"type":"message","bot_id":"B1","text":"answer","ts":"1700000000.000300","blocks":[{"type":"markdown","block_id":"robin_answer:U1","text":"answer"}]},
			{"type":"message","bot_id":"B9","text":"other bot","ts":"1700000000.000400"},
			{"type":"message","user":"U2","text":"after the mention","ts":"1700000000.000900"}
		]}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	got, err := bot.GetThreadMessages(context.Background(), "C0123", "1700000000.000100", "", "1700000000.000500", 50)
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.SlackThreadMessage{
		{TS: "1700000000.000100", UserID: "U1", Text: "parent"},
		{TS: "1700000000.000300", BotID: "B1", FromRobin: true, Text: "answer"},
		{TS: "1700000000.000400", BotID: "B9", Text: "other bot"},
	})

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")
	gt.String(t, reqs[0].Form.Get("ts")).Equal("1700000000.000100")
	gt.String(t, reqs[0].Form.Get("latest")).Equal("1700000000.000500")

	got, err = bot.GetThreadMessages(context.Background(), "C0123", "1700000000.000100", "1700000000.000300", "1700000000.000500", 50)
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.SlackThreadMessage{{TS: "1700000000.000400", BotID: "B9", Text: "other bot"}})

	got, err = bot.GetThreadMessages(context.Background(), "C0123", "1700000000.000100", "", "1700000000.000500", 2)
	gt.NoError(t, err).Required()
	gt.Array(t, got).Length(2).Required()
	gt.String(t, got[0].TS).Equal("1700000000.000300")
}

func TestBot_GetMessage(t *testing.T) {
	cases := map[string]struct {
		body      string
		requester model.SlackUserID
		threadTS  string
		notFound  bool
	}{
		"answer": {
			body:      `{"ok":true,"messages":[{"type":"message","text":"parent","ts":"1700000000.000100","thread_ts":"1700000000.000100"},{"type":"message","bot_id":"B1","ts":"1700000000.000300","thread_ts":"1700000000.000100","blocks":[{"type":"markdown","block_id":"robin_answer:U1","text":"a"}]}]}`,
			requester: "U1", threadTS: "1700000000.000100",
		},
		"progress": {
			body:      `{"ok":true,"messages":[{"type":"message","bot_id":"B1","ts":"1700000000.000300","thread_ts":"1700000000.000100","blocks":[{"type":"context","block_id":"robin_progress:U2","elements":[{"type":"mrkdwn","text":"p"}]}]}]}`,
			requester: "U2", threadTS: "1700000000.000100",
		},
		"not robin": {
			body:     `{"ok":true,"messages":[{"type":"message","user":"U3","text":"hi","ts":"1700000000.000300"}]}`,
			threadTS: "1700000000.000300",
		},
		"empty requester": {
			body:     `{"ok":true,"messages":[{"type":"message","bot_id":"B1","ts":"1700000000.000300","thread_ts":"1700000000.000100","blocks":[{"type":"markdown","block_id":"robin_answer:","text":"a"}]}]}`,
			threadTS: "1700000000.000100",
		},
		"no match":          {body: `{"ok":true,"messages":[{"type":"message","user":"U1","ts":"1700000000.000100"}]}`, notFound: true},
		"thread_not_found":  {body: `{"ok":false,"error":"thread_not_found"}`, notFound: true},
		"message_not_found": {body: `{"ok":false,"error":"message_not_found"}`, notFound: true},
		"not_in_channel":    {body: `{"ok":false,"error":"not_in_channel"}`, notFound: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeSlack(t, map[string]string{"/api/conversations.replies": tc.body})
			bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

			got, err := bot.GetMessage(context.Background(), "C0123", "1700000000.000300")
			if tc.notFound {
				gt.Error(t, err).Is(interfaces.ErrSlackMessageNotFound)
				return
			}
			gt.NoError(t, err).Required()
			gt.Equal(t, *got, model.SlackPostedMessage{TS: "1700000000.000300", ThreadTS: tc.threadTS, Requester: tc.requester})

			reqs := fake.recorded()
			gt.Array(t, reqs).Length(1).Required()
			f := reqs[0].Form
			gt.String(t, f.Get("ts")).Equal("1700000000.000300")
			gt.String(t, f.Get("oldest")).Equal("1700000000.000300")
			gt.String(t, f.Get("latest")).Equal("1700000000.000300")
			gt.String(t, f.Get("inclusive")).Equal("1")
		})
	}
}

func TestBot_DeleteMessage(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.delete": `{"ok":true,"channel":"C0123","ts":"1700000000.000300"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))
	gt.NoError(t, bot.DeleteMessage(context.Background(), "C0123", "1700000000.000300")).Required()
	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Path).Equal("/api/chat.delete")
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")
	gt.String(t, reqs[0].Form.Get("ts")).Equal("1700000000.000300")

	gone := newFakeSlack(t, map[string]string{"/api/chat.delete": `{"ok":false,"error":"message_not_found"}`})
	err := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(gone.apiURL())).DeleteMessage(context.Background(), "C0123", "1.1")
	gt.Error(t, err).Is(interfaces.ErrSlackMessageNotFound)

	denied := newFakeSlack(t, map[string]string{"/api/chat.delete": `{"ok":false,"error":"cant_delete_message"}`})
	err = slack.NewBot("xoxb-test", slackgo.OptionAPIURL(denied.apiURL())).DeleteMessage(context.Background(), "C0123", "1.1")
	gt.Value(t, err).NotNil().Required()
	gt.False(t, errors.Is(err, interfaces.ErrSlackMessageNotFound))
}

func TestBot_PostEphemeral(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postEphemeral": `{"ok":true,"message_ts":"1700000000.000300"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	gt.NoError(t, bot.PostEphemeral(context.Background(), "C0123", "U0123ABCD", "1700000000.000100", "please sign in")).Required()

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Path).Equal("/api/chat.postEphemeral")
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")
	gt.String(t, reqs[0].Form.Get("user")).Equal("U0123ABCD")
	gt.String(t, reqs[0].Form.Get("thread_ts")).Equal("1700000000.000100")
	gt.String(t, reqs[0].Form.Get("text")).Equal("please sign in")
}

func TestBot_PostAnswerError(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postMessage": `{"ok":false,"error":"channel_not_found"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	gt.Value(t, bot.PostAnswer(context.Background(), "C0123", "1.1", "U1", "hello")).NotNil()
}

func TestBot_GetUserName(t *testing.T) {
	cases := map[string]struct {
		body    string
		want    string
		wantErr bool
	}{
		"real name":          {body: `{"ok":true,"user":{"id":"U0123ABCD","name":"alice","real_name":"Alice Example"}}`, want: "Alice Example"},
		"falls back to name": {body: `{"ok":true,"user":{"id":"U0123ABCD","name":"alice","real_name":""}}`, want: "alice"},
		"no name at all":     {body: `{"ok":true,"user":{"id":"U0123ABCD","name":"","real_name":""}}`, wantErr: true},
		"api error":          {body: `{"ok":false,"error":"user_not_found"}`, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeSlack(t, map[string]string{"/api/users.info": tc.body})
			bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

			got, err := bot.GetUserName(context.Background(), "U0123ABCD")
			if tc.wantErr {
				gt.Value(t, err).NotNil()
				return
			}
			gt.NoError(t, err).Required()
			gt.String(t, got).Equal(tc.want)

			reqs := fake.recorded()
			gt.Array(t, reqs).Length(1).Required()
			gt.String(t, reqs[0].Form.Get("user")).Equal("U0123ABCD")
		})
	}
}

func TestBot_PostMessage(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postMessage": `{"ok":true,"channel":"C0123","ts":"1700000000.000100"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	ts, err := bot.PostMessage(context.Background(), "C0123", "U0OWNER", "Good morning")
	gt.NoError(t, err).Required()
	gt.String(t, ts).Equal("1700000000.000100")

	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")
	gt.Bool(t, reqs[0].Form.Has("thread_ts")).False()
	blocks := decodeBlocks(t, reqs[0].Form)
	gt.Array(t, blocks).Length(1).Required()
	gt.String(t, blocks[0].BlockID).Equal("robin_answer:U0OWNER")
	gt.String(t, blocks[0].Text).Equal("Good morning")
}

func TestBot_PostMessageError(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/chat.postMessage": `{"ok":false,"error":"not_in_channel"}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))
	_, err := bot.PostMessage(context.Background(), "C0123", "U0OWNER", "Good morning")
	gt.Value(t, err).NotNil()
}

func TestBot_GetChannel(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/conversations.info": `{"ok":true,"channel":{"id":"C0123","name":"general","is_private":true,"is_archived":true}}`,
	})
	bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL()))

	ch, err := bot.GetChannel(context.Background(), "C0123")
	gt.NoError(t, err).Required()
	gt.Value(t, *ch).Equal(model.SlackChannel{ID: "C0123", Name: "general", IsPrivate: true, IsArchived: true})
	reqs := fake.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Form.Get("channel")).Equal("C0123")

	missing := newFakeSlack(t, map[string]string{
		"/api/conversations.info": `{"ok":false,"error":"channel_not_found"}`,
	})
	_, err = slack.NewBot("xoxb-test", slackgo.OptionAPIURL(missing.apiURL())).GetChannel(context.Background(), "C0123")
	gt.Error(t, err).Is(interfaces.ErrSlackChannelNotFound)

	scope := newFakeSlack(t, map[string]string{
		"/api/conversations.info": `{"ok":false,"error":"missing_scope"}`,
	})
	_, err = slack.NewBot("xoxb-test", slackgo.OptionAPIURL(scope.apiURL())).GetChannel(context.Background(), "C0123")
	gt.Value(t, err).NotNil()
	gt.Bool(t, errors.Is(err, interfaces.ErrSlackChannelNotFound)).False()
}

func TestBot_BotUserID(t *testing.T) {
	fake := newFakeSlack(t, map[string]string{
		"/api/auth.test": `{"ok":true,"user_id":"U0ROBIN","team_id":"T0123"}`,
	})
	id, err := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(fake.apiURL())).BotUserID(context.Background())
	gt.NoError(t, err).Required()
	gt.Value(t, id).Equal(model.SlackUserID("U0ROBIN"))

	empty := newFakeSlack(t, map[string]string{"/api/auth.test": `{"ok":true}`})
	_, err = slack.NewBot("xoxb-test", slackgo.OptionAPIURL(empty.apiURL())).BotUserID(context.Background())
	gt.Value(t, err).NotNil()
}

// membersServer answers conversations.members with the pages in order and
// records the cursors it received.
func membersServer(t *testing.T, pages []string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.NoError(t, r.ParseForm())
		mu.Lock()
		cursors = append(cursors, r.PostForm.Get("cursor"))
		page := pages[len(cursors)-1]
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), cursors...)
	}
}

func TestBot_ChannelMembers(t *testing.T) {
	pages := []string{
		`{"ok":true,"members":["U1","U0ROBIN"],"response_metadata":{"next_cursor":"page2"}}`,
		`{"ok":true,"members":["U2","U0ALICE"],"response_metadata":{"next_cursor":"page3"}}`,
		`{"ok":true,"members":["U3"],"response_metadata":{"next_cursor":""}}`,
	}

	t.Run("stops once every user is found", func(t *testing.T) {
		srv, cursors := membersServer(t, pages)
		bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/api/"))
		got, err := bot.ChannelMembers(context.Background(), "C0123", []model.SlackUserID{"U0ROBIN", "U0ALICE"})
		gt.NoError(t, err).Required()
		gt.Value(t, got).Equal(map[model.SlackUserID]bool{"U0ROBIN": true, "U0ALICE": true})
		gt.Value(t, cursors()).Equal([]string{"", "page2"})
	})

	t.Run("reads every page for a user who is not a member", func(t *testing.T) {
		srv, cursors := membersServer(t, pages)
		bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/api/"))
		got, err := bot.ChannelMembers(context.Background(), "C0123", []model.SlackUserID{"U0ROBIN", "U0BOB"})
		gt.NoError(t, err).Required()
		gt.Value(t, got).Equal(map[model.SlackUserID]bool{"U0ROBIN": true, "U0BOB": false})
		gt.Value(t, cursors()).Equal([]string{"", "page2", "page3"})
	})

	t.Run("api error", func(t *testing.T) {
		srv, _ := membersServer(t, []string{`{"ok":false,"error":"channel_not_found"}`})
		bot := slack.NewBot("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/api/"))
		_, err := bot.ChannelMembers(context.Background(), "C0123", []model.SlackUserID{"U0ROBIN"})
		gt.Value(t, err).NotNil()
	})
}
