package github_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/adapter/github"
	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

func TestUserClient_GetUser(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{
			"GET /user": {status: http.StatusOK, body: `{"id":583231,"login":"octocat","name":"The Octocat"}`},
		})

		identity, err := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").GetUser(context.Background())
		gt.NoError(t, err).Required()
		gt.Value(t, identity).Equal(&model.GitHubIdentity{ID: 583231, Login: "octocat"})

		reqs := f.recorded()
		gt.Array(t, reqs).Length(1).Required()
		gt.String(t, reqs[0].Header.Get("Authorization")).Equal("Bearer ghu_access")
		gt.String(t, reqs[0].Header.Get("Accept")).Equal("application/vnd.github+json")
		gt.String(t, reqs[0].Header.Get("X-GitHub-Api-Version")).Equal("2026-03-10")
	})

	t.Run("rejected token", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{"GET /user": {status: http.StatusUnauthorized, body: `{}`}})
		_, err := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").GetUser(context.Background())
		gt.Error(t, err).Is(interfaces.ErrGitHubTokenInvalid)
	})

	t.Run("server error", func(t *testing.T) {
		f := newFakeGitHub(t, map[string]fakeResponse{"GET /user": {status: http.StatusInternalServerError, body: `{}`}})
		_, err := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").GetUser(context.Background())
		gt.Error(t, err)
		gt.Bool(t, errors.Is(err, interfaces.ErrGitHubTokenInvalid)).False()
	})
}

func TestUserClient_SearchIssues(t *testing.T) {
	f := newFakeGitHub(t, map[string]fakeResponse{
		"GET /search/issues": {status: http.StatusOK, body: `{"total_count":2,"items":[
			{"number":12,"title":"Fix login","state":"open","user":{"login":"alice"},"updated_at":"2026-10-01T00:00:00Z","html_url":"https://github.com/o/r/issues/12","repository_url":"https://api.github.com/repos/o/r"},
			{"number":13,"title":"Add search","state":"closed","user":{"login":"bob"},"updated_at":"2026-10-02T00:00:00Z","html_url":"https://github.com/o/r/pull/13","repository_url":"https://api.github.com/repos/o/r","pull_request":{"url":"x"}}
		]}`},
	})
	got, err := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").SearchIssues(context.Background(), "repo:o/r login", 10)
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.GitHubIssueSummary{
		{Repository: "o/r", Number: 12, Title: "Fix login", State: "open", Author: "alice", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), HTMLURL: "https://github.com/o/r/issues/12"},
		{Repository: "o/r", Number: 13, Title: "Add search", State: "closed", IsPullRequest: true, Author: "bob", UpdatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), HTMLURL: "https://github.com/o/r/pull/13"},
	})
	reqs := f.recorded()
	gt.Array(t, reqs).Length(1).Required()
	gt.String(t, reqs[0].Query.Get("q")).Equal("repo:o/r login")
	gt.String(t, reqs[0].Query.Get("per_page")).Equal("10")
	gt.String(t, reqs[0].Header.Get("Authorization")).Equal("Bearer ghu_access")
}

func TestUserClient_SearchCode(t *testing.T) {
	f := newFakeGitHub(t, map[string]fakeResponse{
		"GET /search/code": {status: http.StatusOK, body: `{"items":[{"path":"pkg/a.go","html_url":"https://github.com/o/r/blob/main/pkg/a.go","repository":{"full_name":"o/r"}}]}`},
	})
	got, err := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").SearchCode(context.Background(), "func Run", 5)
	gt.NoError(t, err).Required()
	gt.Equal(t, got, []model.GitHubCodeHit{{Repository: "o/r", Path: "pkg/a.go", HTMLURL: "https://github.com/o/r/blob/main/pkg/a.go"}})
	gt.String(t, f.recorded()[0].Query.Get("per_page")).Equal("5")
}

func TestUserClient_GetIssue(t *testing.T) {
	f := newFakeGitHub(t, map[string]fakeResponse{
		"GET /repos/o/r/issues/12":          {status: http.StatusOK, body: `{"number":12,"title":"Fix login","state":"open","user":{"login":"alice"},"updated_at":"2026-10-01T00:00:00Z","html_url":"https://github.com/o/r/issues/12","repository_url":"https://api.github.com/repos/o/r","body":"steps","labels":[{"name":"bug"}]}`},
		"GET /repos/o/r/issues/12/comments": {status: http.StatusOK, body: `[{"user":{"login":"bob"},"body":"same here","created_at":"2026-10-02T00:00:00Z"}]`},
	})
	got, err := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").GetIssue(context.Background(), "o", "r", 12)
	gt.NoError(t, err).Required()
	gt.Equal(t, *got, model.GitHubIssue{
		GitHubIssueSummary: model.GitHubIssueSummary{Repository: "o/r", Number: 12, Title: "Fix login", State: "open", Author: "alice", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), HTMLURL: "https://github.com/o/r/issues/12"},
		Body:               "steps",
		Labels:             []string{"bug"},
		Comments:           []model.GitHubComment{{Author: "bob", Body: "same here", CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}},
	})
	reqs := f.recorded()
	gt.Array(t, reqs).Length(2).Required()
	gt.String(t, reqs[1].Query.Get("per_page")).Equal("30")

	_, err = github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access").GetIssue(context.Background(), "o/../x", "r", 12)
	gt.Error(t, err)
}

func TestUserClient_GetContent(t *testing.T) {
	f := newFakeGitHub(t, map[string]fakeResponse{
		"GET /repos/o/r/contents/docs/a.md": {status: http.StatusOK, body: `{"type":"file","name":"a.md","path":"docs/a.md","size":5,"encoding":"base64","content":"aGVs\nbG8=\n","html_url":"https://github.com/o/r/blob/main/docs/a.md"}`},
		"GET /repos/o/r/contents/docs":      {status: http.StatusOK, body: `[{"type":"file","name":"a.md","path":"docs/a.md"},{"type":"dir","name":"img","path":"docs/img"}]`},
		"GET /repos/o/r/contents/big.bin":   {status: http.StatusOK, body: `{"type":"file","name":"big.bin","path":"big.bin","size":2000000,"encoding":"none","content":""}`},
	})
	client := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access")

	file, err := client.GetContent(context.Background(), "o", "r", "docs/a.md", "main")
	gt.NoError(t, err).Required()
	gt.Equal(t, *file, model.GitHubContent{Type: "file", Path: "docs/a.md", Text: "hello", HTMLURL: "https://github.com/o/r/blob/main/docs/a.md"})
	gt.String(t, f.recorded()[0].Query.Get("ref")).Equal("main")

	dir, err := client.GetContent(context.Background(), "o", "r", "docs", "")
	gt.NoError(t, err).Required()
	gt.Equal(t, *dir, model.GitHubContent{Type: "dir", Path: "docs", Entries: []string{"a.md", "img"}})

	_, err = client.GetContent(context.Background(), "o", "r", "big.bin", "")
	gt.Error(t, err)
	_, err = client.GetContent(context.Background(), "o", "r", "../secret", "")
	gt.Error(t, err)
}

func TestUserClient_ReadErrors(t *testing.T) {
	f := newFakeGitHub(t, map[string]fakeResponse{
		"GET /search/issues":       {status: http.StatusUnauthorized, body: `{}`},
		"GET /repos/o/r/issues/99": {status: http.StatusNotFound, body: `{}`},
	})
	client := github.NewUserClientFactoryForTest(f.server.URL).New("ghu_access")
	_, err := client.SearchIssues(context.Background(), "q", 10)
	gt.Error(t, err).Is(interfaces.ErrGitHubTokenInvalid)
	_, err = client.GetIssue(context.Background(), "o", "r", 99)
	gt.Error(t, err).Is(interfaces.ErrGitHubNotFound)
}
