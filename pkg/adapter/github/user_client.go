package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/safe"
)

// UserClientFactory builds REST API clients authenticated with one user's
// access token.
type UserClientFactory struct {
	apiBaseURL string
	httpClient *http.Client
}

var _ interfaces.GitHubUserClientFactory = &UserClientFactory{}

// NewUserClientFactory takes the HTTP client that carries the request timeout.
func NewUserClientFactory(httpClient *http.Client) *UserClientFactory {
	return &UserClientFactory{apiBaseURL: apiBaseURL, httpClient: httpClient}
}

func (f *UserClientFactory) New(token model.GitHubAccessToken) interfaces.GitHubUserClient {
	return &userClient{apiBaseURL: f.apiBaseURL, httpClient: f.httpClient, token: token}
}

type userClient struct {
	apiBaseURL string
	httpClient *http.Client
	token      model.GitHubAccessToken
}

type userResponse struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

func (c *userClient) GetUser(ctx context.Context) (*model.GitHubIdentity, error) {
	var body userResponse
	if err := c.getJSON(ctx, "/user", nil, &body); err != nil {
		return nil, err
	}
	return &model.GitHubIdentity{ID: model.GitHubUserID(body.ID), Login: body.Login}, nil
}

// maxResponseBytes bounds a REST response. A file is read through the
// contents API, which itself returns no content above 1 MB.
const maxResponseBytes = 4 << 20

// getJSON sends GET path?query and decodes the JSON response into out.
func (c *userClient) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := c.apiBaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return goerr.Wrap(err, "failed to build github request", goerr.V("path", path))
	}
	req.Header.Set("Authorization", "Bearer "+string(c.token))
	setAPIHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return goerr.Wrap(err, "github request failed", goerr.V("path", path))
	}
	defer safe.Close(ctx, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return goerr.Wrap(interfaces.ErrGitHubTokenInvalid, "github rejected the user token", goerr.V("status", resp.StatusCode), goerr.V("path", path))
	case http.StatusNotFound:
		return goerr.Wrap(interfaces.ErrGitHubNotFound, "github object not found", goerr.V("path", path))
	default:
		return goerr.New("github request failed", goerr.V("status", resp.StatusCode), goerr.V("path", path))
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return goerr.Wrap(err, "failed to decode github response", goerr.V("path", path))
	}
	return nil
}

var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func repoPath(owner, repo string) (string, error) {
	if !repoNamePattern.MatchString(owner) || !repoNamePattern.MatchString(repo) || owner == ".." || repo == ".." {
		return "", goerr.New("invalid github repository", goerr.V("owner", owner), goerr.V("repo", repo))
	}
	return "/repos/" + owner + "/" + repo, nil
}

type userRef struct {
	Login string `json:"login"`
}

type issueItem struct {
	Number        int             `json:"number"`
	Title         string          `json:"title"`
	State         string          `json:"state"`
	User          userRef         `json:"user"`
	UpdatedAt     time.Time       `json:"updated_at"`
	HTMLURL       string          `json:"html_url"`
	RepositoryURL string          `json:"repository_url"`
	PullRequest   json.RawMessage `json:"pull_request"`
	Body          string          `json:"body"`
	Labels        []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (x issueItem) summary() model.GitHubIssueSummary {
	repo := x.RepositoryURL
	if i := strings.Index(repo, "/repos/"); i >= 0 {
		repo = repo[i+len("/repos/"):]
	}
	return model.GitHubIssueSummary{
		Repository:    repo,
		Number:        x.Number,
		Title:         x.Title,
		State:         x.State,
		IsPullRequest: len(x.PullRequest) > 0 && string(x.PullRequest) != "null",
		Author:        x.User.Login,
		UpdatedAt:     x.UpdatedAt,
		HTMLURL:       x.HTMLURL,
	}
}

func searchQuery(query string, perPage int) url.Values {
	return url.Values{"q": {query}, "per_page": {strconv.Itoa(perPage)}}
}

func (c *userClient) SearchIssues(ctx context.Context, query string, perPage int) ([]model.GitHubIssueSummary, error) {
	var body struct {
		Items []issueItem `json:"items"`
	}
	if err := c.getJSON(ctx, "/search/issues", searchQuery(query, perPage), &body); err != nil {
		return nil, err
	}
	out := make([]model.GitHubIssueSummary, 0, len(body.Items))
	for _, item := range body.Items {
		out = append(out, item.summary())
	}
	return out, nil
}

func (c *userClient) SearchCode(ctx context.Context, query string, perPage int) ([]model.GitHubCodeHit, error) {
	var body struct {
		Items []struct {
			Path       string `json:"path"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/search/code", searchQuery(query, perPage), &body); err != nil {
		return nil, err
	}
	out := make([]model.GitHubCodeHit, 0, len(body.Items))
	for _, item := range body.Items {
		out = append(out, model.GitHubCodeHit{Repository: item.Repository.FullName, Path: item.Path, HTMLURL: item.HTMLURL})
	}
	return out, nil
}

// issueCommentsPerPage is how many comments GetIssue reads.
const issueCommentsPerPage = 30

func (c *userClient) GetIssue(ctx context.Context, owner, repo string, number int) (*model.GitHubIssue, error) {
	base, err := repoPath(owner, repo)
	if err != nil {
		return nil, err
	}
	if number <= 0 {
		return nil, goerr.New("invalid github issue number", goerr.V("number", number))
	}
	issuePath := base + "/issues/" + strconv.Itoa(number)

	var item issueItem
	if err := c.getJSON(ctx, issuePath, nil, &item); err != nil {
		return nil, err
	}
	var comments []struct {
		User      userRef   `json:"user"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := c.getJSON(ctx, issuePath+"/comments", url.Values{"per_page": {strconv.Itoa(issueCommentsPerPage)}}, &comments); err != nil {
		return nil, err
	}

	issue := &model.GitHubIssue{GitHubIssueSummary: item.summary(), Body: item.Body}
	issue.Repository = owner + "/" + repo
	for _, l := range item.Labels {
		issue.Labels = append(issue.Labels, l.Name)
	}
	for _, cm := range comments {
		issue.Comments = append(issue.Comments, model.GitHubComment{Author: cm.User.Login, Body: cm.Body, CreatedAt: cm.CreatedAt})
	}
	return issue, nil
}

type contentItem struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	HTMLURL  string `json:"html_url"`
}

// maxFileBytes is the largest file GetContent returns.
const maxFileBytes = 1 << 20

func (c *userClient) GetContent(ctx context.Context, owner, repo, path, ref string) (*model.GitHubContent, error) {
	base, err := repoPath(owner, repo)
	if err != nil {
		return nil, err
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segments {
		if s == "." || s == ".." {
			return nil, goerr.New("invalid github content path", goerr.V("path", path))
		}
		segments[i] = url.PathEscape(s)
	}
	var query url.Values
	if ref != "" {
		query = url.Values{"ref": {ref}}
	}

	var raw json.RawMessage
	if err := c.getJSON(ctx, base+"/contents/"+strings.Join(segments, "/"), query, &raw); err != nil {
		return nil, err
	}

	if len(raw) > 0 && raw[0] == '[' {
		var entries []contentItem
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, goerr.Wrap(err, "failed to decode github directory")
		}
		dir := &model.GitHubContent{Type: "dir", Path: strings.Trim(path, "/")}
		for _, e := range entries {
			dir.Entries = append(dir.Entries, e.Name)
		}
		return dir, nil
	}

	var file contentItem
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, goerr.Wrap(err, "failed to decode github file")
	}
	if file.Type != "file" {
		return nil, goerr.New("github content is not a file", goerr.V("type", file.Type), goerr.V("path", path))
	}
	if file.Size > maxFileBytes || file.Encoding != "base64" {
		return nil, goerr.New("github file is too large to read", goerr.V("size", file.Size), goerr.V("path", path))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to decode github file content", goerr.V("path", path))
	}
	return &model.GitHubContent{Type: "file", Path: file.Path, Text: string(decoded), HTMLURL: file.HTMLURL}, nil
}
