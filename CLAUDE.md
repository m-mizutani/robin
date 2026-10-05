# CLAUDE.md

Robin is an AI agent that works as a Slack bot and a web UI. The backend is Go,
the frontend is React + TypeScript (Vite, pnpm) embedded into the Go binary.
The code layout follows secmon-lab/hecatoncheires, with a REST API instead of
GraphQL.

## Layers

- `pkg/cli/` — flags, environment variables, dependency wiring, the `serve`
  and `schedule` commands. Each flag is defined once in a flag group of
  `pkg/cli/config` (`SlackApp`, `SlackBot`, `LLM`, `Scheduler`, ...), and a
  command takes the groups it needs; clients built from flags (the bot, the
  Claude client) come from methods of those groups.
- `pkg/controller/http/` — routing, cookies, Slack signature verification, JSON
  responses, SPA serving. Parses input and calls a usecase; no business logic,
  no repository or external API calls.
- `pkg/usecase/` — business operations. `SlackUserAccess` is the only component
  that reads, writes, encrypts, or decrypts Slack user tokens,
  `GoogleWorkspaceAccess` is the only one for Google refresh tokens,
  `NotionAccess` is the only one for Notion tokens (it also runs the Notion
  reads, refreshing the tokens when Notion rejects them), and
  `GitHubUserAccess` is the only one for GitHub user and refresh tokens (it
  refreshes them before they expire, one instance at a time through a lease).
- `pkg/usecase/agents/{name}/` — one package per LLM agent (`mention` answers
  Slack mentions, `hello` posts the scheduled greeting). An agent imports
  `pkg/usecase` and reads the integrations through narrow interfaces it
  defines, which the Access components satisfy; `pkg/usecase` never imports an
  agent and calls it through an interface it defines (`MentionAgent`,
  `Job`). `pkg/cli` wires them.
- Scheduled jobs: a job (`model.JobName`) is a unit of work defined in code,
  not stored. `usecase.Scheduler.RunDue` finds the due job triggers, claims
  and runs them. What starts it (the `schedule` command now, possibly an HTTP
  handler later) only calls `RunDue` and reports its result; it makes no claim
  or run decision. Each job has one `usecase.Job`, built by `newJobs` in
  `pkg/cli/job.go`; a job need not be an agent, and it decides how late it may
  still run (`MaxDelay`).
- `pkg/usecase/usecasetest/` — test doubles of domain interfaces (Slack bot,
  LLM) shared by the tests of `pkg/usecase` and the agents. Imported only by
  tests.
- `pkg/domain/` — models (`model/`, also the Firestore document format) and
  interfaces (`interfaces/`). No I/O.
- `pkg/repository/{firestore,memory}/` — persistence.
- `pkg/adapter/{slack,google,notion,github,kms,claude}/` — thin wrappers that
  implement `domain/interfaces` over an external API. No business decisions.
  `pkg/adapter/localcipher/` replaces KMS only with `--no-auth` and no KMS key.
  `claude` implements the LLM boundary (`interfaces.LLMClient`); the usecase
  layer, agents included, never imports a provider's SDK.
- Every API route is under `/api/v1` (`apiV1Path` in
  `pkg/controller/http/auth.go`); only the SPA, `/hooks/slack/event`, and
  `/hooks/slack/interaction` are outside it.
- `pkg/utils/` — `logging`, `errutil`, `async`, `safe`.

Slack Events API handlers acknowledge within three seconds and run the rest in
`async.Dispatch`.

## Per-user isolation

- Data that belongs to a user lives under `teams/{TeamID}/users/{UserID}`. Build
  those paths only from a `model.UserKey`.
- Repository methods for user data take the user key; do not add methods that
  return more than one user's data (lists, collection-group queries).
- `googleWorkspaceAccounts/{Subject}` records the only user a Google account is
  connected to. It is written and deleted in the same transaction as that
  user's Google credential and is never returned to callers; only
  `AccountInUse` reads whether another user owns it. `notionAccounts/{NotionUserID}`
  does the same for Notion users and Notion credentials, and
  `githubAccounts/{GitHubUserID}` for GitHub accounts and GitHub credentials.
  `agentThreads/{AgentSessionID}` records the only user whose agent
  conversation a Slack thread holds; it is written in the same transaction as
  that user's agent session and only `OwnedByOther` and `Begin` read it.
  `schedules/{JobTriggerID}` holds the owner, the trigger ID and the next run
  time of each job trigger; it is written and deleted in the same transaction
  as the user's job setting (`settings/job`, which holds the triggers), and
  only `JobSettingRepository.ListDue`, used by the scheduler, returns entries
  of more than one user.
- The user key of a request comes only from a verified Slack event or a verified
  web session. A user's token is used only for that same user's requests.
- The KMS additional authenticated data of a token is
  `robin:slack-user-token:v1:{TeamID}:{UserID}` for Slack,
  `robin:google-refresh-token:v1:{TeamID}:{UserID}` for Google,
  `robin:notion-token:v1:{TeamID}:{UserID}` for Notion, and
  `robin:github-access-token:v1:{TeamID}:{UserID}` /
  `robin:github-refresh-token:v1:{TeamID}:{UserID}` for GitHub. Changing any of them
  makes stored tokens undecryptable; add a new version instead.

## Conventions

- Errors: `github.com/m-mizutani/goerr/v2`; wrap with `goerr.Wrap` and attach
  identifiers with `goerr.V`. Never attach tokens, secrets, or message text.
  Discriminate with `errors.Is` / `errors.As`, never by message text.
- An error that is returned is not logged. An error that stops propagating (an
  HTTP handler writing the response, an `async.Dispatch` tail, `cli.Run`) is
  recorded once with `errutil.Handle`, whatever its severity. Tag normal-flow
  errors with `errutil.TagBenign`.
- HTTP error bodies carry a fixed code only (`{"error":"unauthenticated"}`).
- Logging: `logging.From(ctx)`; never the global `slog` functions.
- Close resources with `safe.Close(ctx, c)`; start background work with
  `async.Dispatch`.
- Firestore: store models directly (`Set(ctx, x)` / `DataTo(&x)`); no struct
  tags, no converter types, no composite indexes, hierarchy by subcollections.
  Repositories call `Validate()` before writing and never set timestamps; the
  usecase owns `CreatedAt` / `UpdatedAt`.
- Multiple instances run concurrently. State shared across requests goes to
  Firestore; no package-level maps or caches of business data.
- Default values come from CLI flags, not from internal functions. The
  settings of the TOML file (`--config`) have no flags; their defaults are the
  constants of `pkg/cli/config/file.go`, applied when the file is loaded.
- Source comments and string literals are English. Unexport everything not used
  by another package; test-only access goes through `export_test.go`.

## Tests

- Assertions use `github.com/m-mizutani/gt`. Test packages are `{name}_test`;
  `xyz.go` is tested in `xyz_test.go`.
- Repository tests live in `pkg/repository/*_test.go` and run the same body
  against memory and Firestore via `runRepositoryTest`. The Firestore side uses
  the emulator on `127.0.0.1:28615` (`task test:firestore`) or
  `TEST_FIRESTORE_PROJECT_ID`; it never skips.
- Cloud KMS tests run only when `TEST_GCP_KMS` holds a key name
  (`zenv go test ./...`). `t.Skip` is allowed only for a missing environment
  variable.
- Usecase tests use the memory repository and hand-written fakes, and assert
  stored data and every Slack call, not only the returned error.
- In tests, unset environment variables with `os.Unsetenv`: urfave/cli takes an
  empty variable as the flag value and skips the default.

## Web UI changes: E2E tests

A change to the Web UI (anything under `frontend/src/`, or a server change that
alters what a page does) must come with Playwright E2E tests that cover the
UI's use cases end to end, not only unit tests.

- Tests live in `frontend/e2e/tests/` and run against the real server
  (`frontend/playwright.e2e.config.ts` starts `../robin serve` with
  `--no-auth U0E2ETEST --repository-backend memory`). Do not mock the API
  here; the point is to exercise the server and the page together.
- Cover every use case the user performs on the changed screens, including
  the failure paths the page shows (see `e2e/tests/login.spec.ts` for sign-in:
  redirect when signed out, sign-in, reload, sign-out, forged callback,
  cancelled sign-in).
- Exception: a successful Google Workspace or GitHub connection and the states
  that need one (connected, disconnecting a connected account) require Google
  or GitHub and are covered by unit tests and screenshots instead. E2E still
  covers everything up to the redirect to the provider and every callback that
  needs no real authorization code (`e2e/tests/google-workspace.spec.ts`,
  `e2e/tests/github.spec.ts`).
- Notion runs end to end: Playwright also starts `e2e/fake-notion.mjs`, and
  the server reaches it through `--notion-api-url` (accepted only with
  `--no-auth`). Its `/__control` endpoint chooses the next authorization
  result and returns the recorded requests (`e2e/tests/notion.spec.ts`).
- Run them with `task e2e` (builds the binary, then `pnpm e2e`). CI runs the
  `e2e` job in `.github/workflows/test.yml`; it must pass.
- `--no-auth` makes every sign-in the given user without Slack. It is accepted
  only with the in-memory repository; never add a way to enable it with
  Firestore.

## Web UI changes: screenshots in the pull request

A pull request that changes the Web UI (anything under `frontend/src/` that
alters what is displayed: screens, states, text, styles) must show Playwright
screenshots of every affected screen state in its description. A reviewer
must be able to see the result without running the app.

1. Cover each new or changed screen state with a test in
   `frontend/e2e/screenshots/states.spec.ts`. Mock the API with `page.route`, wait
   until the state is visible, and save `screenshots/<name>.png`. Keep the
   states in step with the screen states listed in the spec.
2. Run `task screenshots` (`pnpm screenshots` in `frontend/`). The images go to
   `frontend/screenshots/`, which is not committed.
3. Attach the PNG files directly to the PR description with `gh`. In the body
   file, write a "Screenshots" section with one entry per state, each a label
   naming the state and a reference to the local file, for example
   `![Login: failed](./frontend/screenshots/login-failed.png)`. Then run, from
   the repository root:

   ```sh
   gh pr edit <number> --body-file <body.md> \
     --attach './frontend/screenshots/login-failed.png#Login: failed' ...
   ```

   `gh` uploads each attached file as an attachment of the PR and rewrites the
   matching reference in the body to the uploaded asset (`gh pr create` takes
   the same `--attach` flag). Check the rendered description afterwards.
4. Never commit screenshots, and never push them to any branch (including a
   dedicated one) or other place inside the repository.

If the screenshots cannot be taken, say so in the PR description and in the
report instead of treating the change as verified.

## Checks before finishing

- `go vet ./...`, `gofmt`, `go test ./...` (with the Firestore emulator)
- In `frontend/`: `pnpm lint`, `pnpm test`, `pnpm build`
- For a Web UI change: `task e2e`, `task screenshots`, and the screenshots in
  the PR description (see the two sections above)
- Update `docs/setup.md` when flags, environment variables, Slack scopes, IAM
  roles, or Firestore paths change, and `docs/slack-app-manifest.yaml` when
  Slack scopes or events change.
- Commit messages: one line, `<type>: <subject>`, English.
