import { defineConfig, devices } from '@playwright/test'

// End-to-end tests against the real robin server. The server runs with
// --no-auth, so a sign-in goes through the real state cookie, callback,
// session, and cookie handling without contacting Slack, and becomes the user
// below. Build the binary first (`task build`), or point ROBIN_BIN at it.
const port = 18081
const baseURL = `http://127.0.0.1:${port}`
const binary = process.env.ROBIN_BIN ?? '../robin'

export const e2eTeamID = 'T0E2ETEST'
export const e2eUserID = 'U0E2ETEST'
// A Google OAuth client that does not exist. It enables the Google Workspace
// endpoints; the tests stop the browser before it reaches Google.
export const e2eGoogleClientID = 'e2e-client.apps.googleusercontent.com'
// A GitHub App that does not exist. It enables the GitHub endpoints; the
// tests stop the browser before it reaches GitHub.
export const e2eGitHubClientID = 'Iv1.e2e0000000000000'

// Notion is served by e2e/fake-notion.mjs, so a connection runs from the
// authorization to the token exchange and the revocation.
export const fakeNotionURL = 'http://127.0.0.1:18082'
export const e2eNotionClientID = 'e2e-notion-client'
const e2eNotionClientSecret = 'e2e-notion-secret'
export const e2eNotionWorkspaceID = '0f4a2b1c-3d4e-4f50-8a6b-7c8d9e0f1a2b'

// The Slack Web API of the bot is served by e2e/fake-slack.mjs, so adding a
// scheduled message runs the channel checks against it. A bot token needs a
// signing secret with --no-auth, and Slack events need an LLM credential; the
// tests send no events, so the API key is never used.
export const fakeSlackURL = 'http://127.0.0.1:18083'

export default defineConfig({
  testDir: './e2e/tests',
  outputDir: './test-results/e2e',
  // The server keeps state in memory and the tests share it; run them in one
  // worker so each test's sign-in state is its own browser context only.
  workers: 1,
  reporter: 'list',
  use: {
    ...devices['Desktop Chrome'],
    baseURL,
  },
  webServer: [
    {
      command: 'node e2e/fake-notion.mjs',
      env: {
        FAKE_NOTION_PORT: '18082',
        FAKE_NOTION_CLIENT_ID: e2eNotionClientID,
        FAKE_NOTION_CLIENT_SECRET: e2eNotionClientSecret,
        FAKE_NOTION_WORKSPACE_ID: e2eNotionWorkspaceID,
      },
      url: `${fakeNotionURL}/__control`,
      reuseExistingServer: false,
    },
    {
      command: 'node e2e/fake-slack.mjs',
      env: { FAKE_SLACK_PORT: '18083', FAKE_SLACK_USER_ID: e2eUserID },
      url: `${fakeSlackURL}/__control`,
      reuseExistingServer: false,
    },
    {
      command: [
        binary,
        '--log-format json',
        'serve',
        `--addr 127.0.0.1:${port}`,
        `--base-url ${baseURL}`,
        '--repository-backend memory',
        `--slack-team-id ${e2eTeamID}`,
        `--no-auth ${e2eUserID}`,
        `--google-client-id ${e2eGoogleClientID}`,
        '--google-client-secret e2e-client-secret',
        `--notion-client-id ${e2eNotionClientID}`,
        `--notion-client-secret ${e2eNotionClientSecret}`,
        `--notion-workspace-id ${e2eNotionWorkspaceID}`,
        `--notion-api-url ${fakeNotionURL}`,
        `--github-client-id ${e2eGitHubClientID}`,
        '--github-client-secret e2e-client-secret',
        '--slack-bot-token xoxb-e2e',
        '--slack-signing-secret e2e-signing-secret',
        '--anthropic-api-key e2e-unused',
        `--slack-api-url ${fakeSlackURL}/api/`,
      ].join(' '),
      url: `${baseURL}/login`,
      reuseExistingServer: false,
    },
  ],
})
