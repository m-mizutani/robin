package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
)

type githubSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type githubIssueInput struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

type githubFileInput struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Path  string `json:"path"`
	Ref   string `json:"ref"`
}

// callGitHub runs fn with a client of the user's token, refreshed when needed.
func callGitHub[T any](ctx context.Context, github *GitHubUserAccess, req AgentRequest, fn func(interfaces.GitHubUserClient) (T, error)) (string, error) {
	client, err := github.Client(ctx, req.Key)
	if err != nil {
		return "", err
	}
	v, err := fn(client)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

func githubTools(github *GitHubUserAccess) []*agentTool {
	searchSchema := `{"type":"object","properties":{
		"query":{"type":"string","description":"GitHub search query, for example ` + "`repo:owner/name is:open label:bug`" + `."},
		"max_results":{"type":"integer","minimum":1,"maximum":30,"description":"Number of results, 10 when omitted."}
	},"required":["query"]}`
	decodeSearch := func(input json.RawMessage) (githubSearchInput, int, error) {
		in, err := decodeInput[githubSearchInput](input)
		if err != nil {
			return in, 0, err
		}
		if err := requireString("query", in.Query); err != nil {
			return in, 0, err
		}
		n, err := intInRange("max_results", in.MaxResults, 10, 1, 30)
		return in, n, err
	}

	return []*agentTool{
		{
			service: "GitHub",
			spec:    modelToolSpec("github_search_issues", "Search the GitHub issues and pull requests the requester can read.", searchSchema),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[githubSearchInput](input)
				return "Searching GitHub issues for " + quoted(in.Query)
			},
			run: func(ctx context.Context, req AgentRequest, input json.RawMessage) (string, error) {
				in, n, err := decodeSearch(input)
				if err != nil {
					return "", err
				}
				return callGitHub(ctx, github, req, func(c interfaces.GitHubUserClient) (any, error) {
					return c.SearchIssues(ctx, in.Query, n)
				})
			},
		},
		{
			service: "GitHub",
			spec:    modelToolSpec("github_search_code", "Search code in the GitHub repositories the requester can read.", searchSchema),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[githubSearchInput](input)
				return "Searching GitHub code for " + quoted(in.Query)
			},
			run: func(ctx context.Context, req AgentRequest, input json.RawMessage) (string, error) {
				in, n, err := decodeSearch(input)
				if err != nil {
					return "", err
				}
				return callGitHub(ctx, github, req, func(c interfaces.GitHubUserClient) (any, error) {
					return c.SearchCode(ctx, in.Query, n)
				})
			},
		},
		{
			service: "GitHub",
			spec: modelToolSpec("github_get_issue", "Read a GitHub issue or pull request with its first 30 comments.",
				`{"type":"object","properties":{
					"owner":{"type":"string"},
					"repo":{"type":"string"},
					"number":{"type":"integer","minimum":1}
				},"required":["owner","repo","number"]}`),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[githubIssueInput](input)
				return fmt.Sprintf("Reading %s/%s#%d", in.Owner, in.Repo, in.Number)
			},
			run: func(ctx context.Context, req AgentRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[githubIssueInput](input)
				if err != nil {
					return "", err
				}
				if err := requireString("owner", in.Owner); err != nil {
					return "", err
				}
				if err := requireString("repo", in.Repo); err != nil {
					return "", err
				}
				if in.Number <= 0 {
					return "", inputError("Invalid input: number must be a positive integer.")
				}
				return callGitHub(ctx, github, req, func(c interfaces.GitHubUserClient) (any, error) {
					return c.GetIssue(ctx, in.Owner, in.Repo, in.Number)
				})
			},
		},
		{
			service: "GitHub",
			spec: modelToolSpec("github_get_file", "Read a file of a GitHub repository, or list a directory.",
				`{"type":"object","properties":{
					"owner":{"type":"string"},
					"repo":{"type":"string"},
					"path":{"type":"string","description":"Path in the repository; empty for the root."},
					"ref":{"type":"string","description":"Branch, tag or commit; the default branch when omitted."}
				},"required":["owner","repo","path"]}`),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[githubFileInput](input)
				return fmt.Sprintf("Reading %s/%s/%s", in.Owner, in.Repo, truncateText(in.Path, progressQueryChars))
			},
			run: func(ctx context.Context, req AgentRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[githubFileInput](input)
				if err != nil {
					return "", err
				}
				if err := requireString("owner", in.Owner); err != nil {
					return "", err
				}
				if err := requireString("repo", in.Repo); err != nil {
					return "", err
				}
				return callGitHub(ctx, github, req, func(c interfaces.GitHubUserClient) (any, error) {
					return c.GetContent(ctx, in.Owner, in.Repo, in.Path, in.Ref)
				})
			},
		},
	}
}
