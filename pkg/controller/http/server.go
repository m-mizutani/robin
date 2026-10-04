package http

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/m-mizutani/goerr/v2"
	"github.com/slack-go/slack/slackevents"

	"github.com/m-mizutani/robin/frontend"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/domain/model/auth"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/safe"
)

type authUseCase interface {
	AuthorizeURL(state string) string
	HandleCallback(ctx context.Context, code string) (*auth.Session, auth.SessionSecret, error)
	Authenticate(ctx context.Context, id auth.SessionID, secret auth.SessionSecret) (*auth.Session, error)
	Logout(ctx context.Context, id auth.SessionID, secret auth.SessionSecret) error
	Me(ctx context.Context, key model.UserKey) (*usecase.Me, error)
}

type slackEventUseCase interface {
	HandleEvent(ctx context.Context, event *slackevents.EventsAPIEvent) error
	HandleMessageShortcut(ctx context.Context, s model.SlackMessageShortcut) error
}

type googleWorkspaceUseCase interface {
	AuthorizeURL(ctx context.Context, key model.UserKey, state string) (string, error)
	HandleCallback(ctx context.Context, key model.UserKey, code string) error
	Status(ctx context.Context, key model.UserKey) (*usecase.GoogleWorkspaceStatus, error)
	Disconnect(ctx context.Context, key model.UserKey) error
}

type notionUseCase interface {
	AuthorizeURL(ctx context.Context, key model.UserKey, state string) (string, error)
	HandleCallback(ctx context.Context, key model.UserKey, code string) error
	Status(ctx context.Context, key model.UserKey) (*usecase.NotionStatus, error)
	Disconnect(ctx context.Context, key model.UserKey) error
}

type gitHubUseCase interface {
	AuthorizeURL(ctx context.Context, key model.UserKey, state, codeVerifier string) (string, error)
	HandleCallback(ctx context.Context, key model.UserKey, code, codeVerifier string) error
	Status(ctx context.Context, key model.UserKey) (*usecase.GitHubStatus, error)
	Disconnect(ctx context.Context, key model.UserKey) error
}

type jobUseCase interface {
	List(ctx context.Context, key model.UserKey) (*usecase.JobList, error)
	Create(ctx context.Context, key model.UserKey, in usecase.JobInput) (*model.Job, error)
	Delete(ctx context.Context, key model.UserKey, id model.JobID) error
}

type Config struct {
	// BaseURL decides the Secure cookie attribute. A TLS-terminating proxy
	// hides TLS from the request, so the scheme of the public URL is used.
	BaseURL string
	// Static is the SPA file system. nil serves the embedded frontend build.
	Static fs.FS
}

type Server struct {
	router       *chi.Mux
	authUC       authUseCase
	slackUC      slackEventUseCase
	googleUC     googleWorkspaceUseCase
	notionUC     notionUseCase
	githubUC     gitHubUseCase
	jobUC        jobUseCase
	secureCookie bool
}

type Option func(*options)

type options struct {
	slackUC            slackEventUseCase
	slackSigningSecret string
	googleUC           googleWorkspaceUseCase
	notionUC           notionUseCase
	githubUC           gitHubUseCase
	jobUC              jobUseCase
}

// WithJobs mounts POST /api/v1/jobs and DELETE /api/v1/jobs/{jobID}. Without
// it, GET /api/v1/jobs reports the feature as unavailable.
func WithJobs(uc jobUseCase) Option {
	return func(o *options) {
		o.jobUC = uc
	}
}

// WithNotion mounts the connect, callback, and disconnect endpoints of the
// Notion integration. Without it, those endpoints do not exist and the status
// endpoint reports the integration as unavailable.
func WithNotion(uc notionUseCase) Option {
	return func(o *options) {
		o.notionUC = uc
	}
}

// WithGitHub mounts the connect, callback, and disconnect endpoints of the
// GitHub integration. Without it, those endpoints do not exist and the status
// endpoint reports the integration as unavailable.
func WithGitHub(uc gitHubUseCase) Option {
	return func(o *options) {
		o.githubUC = uc
	}
}

// WithGoogleWorkspace mounts the connect, callback, and disconnect endpoints
// of the Google Workspace integration. Without it, those endpoints do not
// exist and the status endpoint reports the integration as unavailable.
func WithGoogleWorkspace(uc googleWorkspaceUseCase) Option {
	return func(o *options) {
		o.googleUC = uc
	}
}

// WithSlackEvents mounts POST /hooks/slack/event and POST
// /hooks/slack/interaction. Without it the endpoints do not exist, which is
// how a no-auth development server without Slack runs.
func WithSlackEvents(uc slackEventUseCase, signingSecret string) Option {
	return func(o *options) {
		o.slackUC = uc
		o.slackSigningSecret = signingSecret
	}
}

func New(authUC authUseCase, cfg Config, opts ...Option) (*Server, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.slackUC != nil && o.slackSigningSecret == "" {
		return nil, goerr.New("slack events need a signing secret")
	}

	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", cfg.BaseURL))
	}

	static := cfg.Static
	if static == nil {
		static, err = fs.Sub(frontend.StaticFiles, "dist")
		if err != nil {
			return nil, goerr.Wrap(err, "failed to open embedded frontend")
		}
	}

	s := &Server{
		router:       chi.NewRouter(),
		authUC:       authUC,
		slackUC:      o.slackUC,
		googleUC:     o.googleUC,
		notionUC:     o.notionUC,
		githubUC:     o.githubUC,
		jobUC:        o.jobUC,
		secureCookie: base.Scheme == "https",
	}

	r := s.router
	r.Use(middleware.RequestID)
	r.Use(accessLogger)
	r.Use(middleware.Recoverer)

	apiNotFound := func(w http.ResponseWriter, r *http.Request) {
		writeError(r.Context(), w, http.StatusNotFound, errCodeNotFound)
	}
	r.Route(authPath, func(r chi.Router) {
		r.Get("/login", s.authLoginHandler)
		r.Get("/callback", s.authCallbackHandler)
		r.Post("/logout", s.authLogoutHandler)
		r.With(requireSession(authUC)).Get("/me", s.authMeHandler)
		r.NotFound(apiNotFound)
	})
	r.Route(googleStateCookiePath, func(r chi.Router) {
		r.With(requireSession(authUC)).Get("/", s.googleStatusHandler)
		if s.googleUC != nil {
			r.With(requireSessionOrLogin(authUC)).Get("/connect", s.googleConnectHandler)
			r.With(requireSessionOrLogin(authUC)).Get("/callback", s.googleCallbackHandler)
			r.With(requireSession(authUC)).Post("/disconnect", s.googleDisconnectHandler)
		}
		r.NotFound(apiNotFound)
	})
	r.Route(notionStateCookiePath, func(r chi.Router) {
		r.With(requireSession(authUC)).Get("/", s.notionStatusHandler)
		if s.notionUC != nil {
			r.With(requireSessionOrLogin(authUC)).Get("/connect", s.notionConnectHandler)
			r.With(requireSessionOrLogin(authUC)).Get("/callback", s.notionCallbackHandler)
			r.With(requireSession(authUC)).Post("/disconnect", s.notionDisconnectHandler)
		}
		r.NotFound(apiNotFound)
	})
	r.Route(githubStateCookiePath, func(r chi.Router) {
		r.With(requireSession(authUC)).Get("/", s.githubStatusHandler)
		if s.githubUC != nil {
			r.With(requireSessionOrLogin(authUC)).Get("/connect", s.githubConnectHandler)
			r.With(requireSessionOrLogin(authUC)).Get("/callback", s.githubCallbackHandler)
			r.With(requireSession(authUC)).Post("/disconnect", s.githubDisconnectHandler)
		}
		r.NotFound(apiNotFound)
	})
	r.Route(jobsPath, func(r chi.Router) {
		r.With(requireSession(authUC)).Get("/", s.jobsListHandler)
		if s.jobUC != nil {
			r.With(requireSession(authUC)).Post("/", s.jobsCreateHandler)
			r.With(requireSession(authUC)).Delete("/{jobID}", s.jobsDeleteHandler)
		}
		r.NotFound(apiNotFound)
		r.MethodNotAllowed(apiNotFound)
	})
	r.HandleFunc("/api/*", apiNotFound)

	if o.slackUC != nil {
		r.With(slackSignatureMiddleware(o.slackSigningSecret)).Post("/hooks/slack/event", s.slackEventHandler)
		r.With(slackSignatureMiddleware(o.slackSigningSecret)).Post("/hooks/slack/interaction", s.slackInteractionHandler)
	}

	r.Get("/*", spaHandler(static))

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// spaHandler serves a file when it exists and index.html otherwise, so the
// client-side router handles paths such as /login.
func spaHandler(static fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(static))
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := static.Open(path); err == nil {
				safe.Close(r.Context(), f)
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		index, err := static.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer safe.Close(r.Context(), index)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		safe.Copy(r.Context(), w, index)
	}
}
