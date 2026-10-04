import { readFile } from 'node:fs/promises'
import { extname, join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'

// One test per screen state listed in the spec. Each test mocks the API the
// state depends on, waits for the state to be visible, and saves
// screenshots/<name>.png.

const distDir = join(process.cwd(), 'dist')
const contentTypes: Record<string, string> = {
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
}

// Serve the built SPA the way the Go server does: a file when it exists,
// index.html otherwise. Tests register their API mocks afterwards, and
// Playwright tries the most recently registered route first.
test.beforeEach(async ({ page }) => {
  await page.route('http://robin.test/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.startsWith('/api/')) {
      await route.fulfill({ status: 404, contentType: 'application/json', body: '{"error":"not_found"}' })
      return
    }
    const file = path === '/' ? 'index.html' : path.slice(1)
    try {
      const body = await readFile(join(distDir, file))
      await route.fulfill({ status: 200, contentType: contentTypes[extname(file)] ?? 'application/octet-stream', body })
    } catch {
      const body = await readFile(join(distDir, 'index.html'))
      await route.fulfill({ status: 200, contentType: 'text/html', body })
    }
  })
  // Notion and GitHub are not connected, and there are no scheduled
  // messages, unless a test mocks them.
  await mockNotion(page, 200, notionNotConnected)
  await mockGitHub(page, 200, githubNotConnected)
  await mockJobs(page, 200, jobsStatus([]))
})

// Resolved against the working directory, which is frontend/ for `pnpm screenshots`.
const shot = (name: string) => ({ path: `screenshots/${name}.png`, fullPage: true })

const me = (connected: boolean) =>
  JSON.stringify({ team_id: 'T0123ABCD', user_id: 'U0123ABCD', name: 'Alice Example', slack_connected: connected })

// Some states are captured while a request is still pending, so those route
// handlers never answer. Drop them after each test; otherwise closing the
// page waits for them forever.
test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: 'ignoreErrors' })
})

async function mockMe(page: Page, status: number, body: string) {
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({ status, contentType: 'application/json', body }),
  )
}

async function signedOut(page: Page) {
  await mockMe(page, 401, '{"error":"unauthenticated"}')
}

const googleNotConnected = { available: true, connected: false, email: '' }
const googleConnected = { available: true, connected: true, email: 'alice@example.com' }

async function mockGoogle(page: Page, status: number, body: object) {
  await page.route('**/api/v1/integrations/google-workspace', (route) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) }),
  )
}

// The settings page of a user linked to Slack, with the given Google Workspace
// status.
async function settingsWithGoogle(page: Page, google: object) {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, google)
}

const notionNotConnected = {
  available: true,
  connected: false,
  needs_reconnect: false,
  user_name: '',
  workspace_name: '',
}
const notionConnected = { ...notionNotConnected, connected: true, user_name: 'Alice Example', workspace_name: 'Acme' }

async function mockNotion(page: Page, status: number, body: object) {
  await page.route('**/api/v1/integrations/notion', (route) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) }),
  )
}

// The settings page of a user linked to Slack, without Google Workspace, and
// with the given Notion status.
async function settingsWithNotion(page: Page, notion: object) {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await mockNotion(page, 200, notion)
}

const githubNotConnected = { available: true, connected: false, login: '' }
const githubConnected = { available: true, connected: true, login: 'octocat' }

async function mockGitHub(page: Page, status: number, body: object) {
  await page.route('**/api/v1/integrations/github', (route) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) }),
  )
}

type LastRun = {
  status: string
  failure: string
  scheduled_at: string
  deadline: string | null
  finished_at: string | null
}

function scheduledJob(n: number, channel: string, lastRun: LastRun | null) {
  return {
    id: `00000000-0000-4000-8000-00000000000${n}`,
    kind: 'hello',
    channel_id: `C0${channel.toUpperCase().replace(/[^A-Z0-9]/g, '')}`,
    channel_name: channel,
    hour: 9,
    minute: 0,
    time_zone: 'Asia/Tokyo',
    next_run_at: '2026-10-05T00:00:00Z',
    last_run: lastRun,
  }
}

function jobsStatus(jobs: object[], overrides: object = {}) {
  return { available: true, max_jobs: 10, jobs, ...overrides }
}

// Answers GET /api/v1/jobs only; POST to the same path goes to the routes a
// test registers.
async function mockJobs(page: Page, status: number, body: object) {
  await page.route('**/api/v1/jobs', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
      : route.fallback(),
  )
}

// The settings page of a user linked to Slack, without Google Workspace or
// Notion, and with the given GitHub status.
async function settingsWithGitHub(page: Page, github: object) {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await mockGitHub(page, 200, github)
}

test('login: initial', async ({ page }) => {
  await signedOut(page)
  await page.goto('/login')
  await expect(page.getByRole('button', { name: 'Sign in with Slack' })).toBeVisible()
  await page.screenshot(shot('login-initial'))
})

test('login: redirecting to Slack', async ({ page }) => {
  await signedOut(page)
  // Answer the navigation to /api/v1/auth/login with 204: the browser keeps the
  // current document, so the page stays in its redirecting state. A request
  // left pending instead keeps the navigation open and blocks the test.
  await page.route('**/api/v1/auth/login', (route) => route.fulfill({ status: 204 }))
  await page.goto('/login')
  // The click starts a navigation that never completes; do not wait for it.
  await page.getByRole('button', { name: 'Sign in with Slack' }).click({ noWaitAfter: true })
  await expect(page.getByRole('button', { name: 'Redirecting to Slack…' })).toBeDisabled()
  await page.screenshot(shot('login-redirecting'))
})

test('login: cancelled at Slack', async ({ page }) => {
  await signedOut(page)
  await page.goto('/login?error=access_denied')
  await expect(page.getByRole('alert')).toContainText('Sign-in was cancelled.')
  await page.screenshot(shot('login-cancelled'))
})

test('login: failed', async ({ page }) => {
  await signedOut(page)
  await page.goto('/login?error=login_failed')
  await expect(page.getByRole('alert')).toContainText('Sign-in failed.')
  await page.screenshot(shot('login-failed'))
})

test('sign-in check: loading', async ({ page }) => {
  await page.route('**/api/v1/auth/me', () => new Promise(() => {}))
  await page.goto('/')
  await expect(page.getByText('Loading…')).toBeVisible()
  await page.screenshot(shot('check-loading'))
})

test('sign-in check: failed', async ({ page }) => {
  await mockMe(page, 500, '{"error":"internal_error"}')
  await page.goto('/')
  await expect(page.getByRole('alert')).toContainText('Could not check your sign-in status.')
  await page.screenshot(shot('check-failed'))
})

test('settings: Slack connected', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await page.goto('/settings')
  await expect(page.getByRole('listitem', { name: 'Slack' }).getByText('Connected')).toBeVisible()
  await page.screenshot(shot('settings-slack-connected'))
})

test('settings: Slack not connected', async ({ page }) => {
  await mockMe(page, 200, me(false))
  await mockGoogle(page, 200, googleNotConnected)
  await page.goto('/settings')
  await expect(page.getByRole('button', { name: 'Connect Slack' })).toBeEnabled()
  await page.screenshot(shot('settings-slack-not-connected'))
})

test('settings: connecting Slack', async ({ page }) => {
  await mockMe(page, 200, me(false))
  await mockGoogle(page, 200, googleNotConnected)
  // See 'login: redirecting to Slack': a 204 keeps the current document.
  await page.route('**/api/v1/auth/login', (route) => route.fulfill({ status: 204 }))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Connect Slack' }).click({ noWaitAfter: true })
  await expect(page.getByRole('button', { name: 'Redirecting to Slack…' })).toBeDisabled()
  await page.screenshot(shot('settings-connecting-slack'))
})

test('settings: signing out', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await page.route('**/api/v1/auth/logout', () => new Promise(() => {}))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('button', { name: 'Signing out…' })).toBeDisabled()
  await page.screenshot(shot('settings-signing-out'))
})

test('settings: sign-out failed', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await page.route('**/api/v1/auth/logout', (route) =>
    route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"internal_error"}' }),
  )
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('alert')).toContainText('Could not sign out.')
  await page.screenshot(shot('settings-sign-out-failed'))
})

const googleRow = (page: Page) => page.getByRole('listitem', { name: 'Google Workspace' })

test('settings: Google Workspace status being checked', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await page.route('**/api/v1/integrations/google-workspace', () => new Promise(() => {}))
  await page.goto('/settings')
  await expect(googleRow(page).getByText('Checking…')).toBeVisible()
  await page.screenshot(shot('settings-google-checking'))
})

test('settings: Google Workspace status check failed', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 500, { error: 'internal_error' })
  await page.goto('/settings')
  await expect(googleRow(page).getByRole('button', { name: 'Check Google Workspace again' })).toBeVisible()
  await page.screenshot(shot('settings-google-check-failed'))
})

test('settings: Google Workspace not available', async ({ page }) => {
  await settingsWithGoogle(page, { available: false, connected: false, email: '' })
  await page.goto('/settings')
  await expect(googleRow(page).getByText('Not available')).toBeVisible()
  await page.screenshot(shot('settings-google-unavailable'))
})

test('settings: Google Workspace not connected', async ({ page }) => {
  await settingsWithGoogle(page, googleNotConnected)
  await page.goto('/settings')
  await expect(page.getByRole('button', { name: 'Connect Google Workspace' })).toBeEnabled()
  await page.screenshot(shot('settings-google-not-connected'))
})

test('settings: connecting Google Workspace', async ({ page }) => {
  await settingsWithGoogle(page, googleNotConnected)
  // See 'login: redirecting to Slack': a 204 keeps the current document.
  await page.route('**/api/v1/integrations/google-workspace/connect', (route) => route.fulfill({ status: 204 }))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Connect Google Workspace' }).click({ noWaitAfter: true })
  await expect(page.getByRole('button', { name: 'Redirecting to Google Workspace…' })).toBeDisabled()
  await page.screenshot(shot('settings-google-connecting'))
})

test('settings: Google Workspace connected', async ({ page }) => {
  await settingsWithGoogle(page, googleConnected)
  await page.goto('/settings')
  await expect(googleRow(page).getByText('Account: alice@example.com')).toBeVisible()
  await page.screenshot(shot('settings-google-connected'))
})

test('settings: disconnecting Google Workspace', async ({ page }) => {
  await settingsWithGoogle(page, googleConnected)
  await page.route('**/api/v1/integrations/google-workspace/disconnect', () => new Promise(() => {}))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disconnect Google Workspace' }).click()
  await expect(page.getByRole('button', { name: 'Disconnecting…' })).toBeDisabled()
  await page.screenshot(shot('settings-google-disconnecting'))
})

test('settings: disconnecting Google Workspace failed', async ({ page }) => {
  await settingsWithGoogle(page, googleConnected)
  await page.route('**/api/v1/integrations/google-workspace/disconnect', (route) =>
    route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"internal_error"}' }),
  )
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disconnect Google Workspace' }).click()
  await expect(googleRow(page).getByRole('alert')).toContainText('Could not disconnect Google Workspace.')
  await page.screenshot(shot('settings-google-disconnect-failed'))
})

for (const [result, name, text] of [
  ['connected', 'settings-google-notice-connected', 'Google Workspace is connected.'],
  ['access_denied', 'settings-google-notice-access-denied', 'you cancelled the request on Google'],
  ['missing_scope', 'settings-google-notice-missing-scope', 'you did not allow every requested permission'],
  ['account_in_use', 'settings-google-notice-account-in-use', 'already connected to another Robin user'],
  ['failed', 'settings-google-notice-failed', 'Could not connect Google Workspace.'],
] as const) {
  test(`settings: Google Workspace connection result ${result}`, async ({ page }) => {
    await settingsWithGoogle(page, result === 'connected' ? googleConnected : googleNotConnected)
    await page.goto(`/settings?google_workspace=${result}`)
    await expect(page.getByText(text)).toBeVisible()
    await page.screenshot(shot(name))
  })
}

const notionRow = (page: Page) => page.getByRole('listitem', { name: 'Notion' })

test('settings: Notion status being checked', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await page.route('**/api/v1/integrations/notion', () => new Promise(() => {}))
  await page.goto('/settings')
  await expect(notionRow(page).getByText('Checking…')).toBeVisible()
  await page.screenshot(shot('settings-notion-checking'))
})

test('settings: Notion status check failed', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await mockNotion(page, 500, { error: 'internal_error' })
  await page.goto('/settings')
  await expect(notionRow(page).getByRole('button', { name: 'Check Notion again' })).toBeVisible()
  await page.screenshot(shot('settings-notion-check-failed'))
})

test('settings: Notion not available', async ({ page }) => {
  await settingsWithNotion(page, { ...notionNotConnected, available: false })
  await page.goto('/settings')
  await expect(notionRow(page).getByText('Not available')).toBeVisible()
  await page.screenshot(shot('settings-notion-unavailable'))
})

test('settings: Notion not connected', async ({ page }) => {
  await settingsWithNotion(page, notionNotConnected)
  await page.goto('/settings')
  await expect(page.getByRole('button', { name: 'Connect Notion' })).toBeEnabled()
  await page.screenshot(shot('settings-notion-not-connected'))
})

test('settings: connecting Notion', async ({ page }) => {
  await settingsWithNotion(page, notionNotConnected)
  // See 'login: redirecting to Slack': a 204 keeps the current document.
  await page.route('**/api/v1/integrations/notion/connect', (route) => route.fulfill({ status: 204 }))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Connect Notion' }).click({ noWaitAfter: true })
  await expect(page.getByRole('button', { name: 'Redirecting to Notion…' })).toBeDisabled()
  await page.screenshot(shot('settings-notion-connecting'))
})

test('settings: Notion connected', async ({ page }) => {
  await settingsWithNotion(page, notionConnected)
  await page.goto('/settings')
  await expect(notionRow(page).getByText('Account: Alice Example (Acme)')).toBeVisible()
  await page.screenshot(shot('settings-notion-connected'))
})

test('settings: disconnecting Notion', async ({ page }) => {
  await settingsWithNotion(page, notionConnected)
  await page.route('**/api/v1/integrations/notion/disconnect', () => new Promise(() => {}))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disconnect Notion' }).click()
  await expect(notionRow(page).getByRole('button', { name: 'Disconnecting…' })).toBeDisabled()
  await page.screenshot(shot('settings-notion-disconnecting'))
})

test('settings: disconnecting Notion failed', async ({ page }) => {
  await settingsWithNotion(page, notionConnected)
  await page.route('**/api/v1/integrations/notion/disconnect', (route) =>
    route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"internal_error"}' }),
  )
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disconnect Notion' }).click()
  await expect(notionRow(page).getByRole('alert')).toContainText('Could not disconnect Notion.')
  await page.screenshot(shot('settings-notion-disconnect-failed'))
})

test('settings: Notion needs to be reconnected', async ({ page }) => {
  await settingsWithNotion(page, { ...notionConnected, needs_reconnect: true })
  await page.goto('/settings')
  await expect(notionRow(page).getByRole('button', { name: 'Reconnect Notion' })).toBeEnabled()
  await page.screenshot(shot('settings-notion-needs-reconnect'))
})

for (const [result, name, text] of [
  ['connected', 'settings-notion-notice-connected', 'Notion is connected.'],
  ['access_denied', 'settings-notion-notice-access-denied', 'you cancelled the request on Notion'],
  ['wrong_workspace', 'settings-notion-notice-wrong-workspace', 'the workspace you chose is not the one Robin is set up for'],
  ['account_in_use', 'settings-notion-notice-account-in-use', 'this Notion account is already connected'],
  ['failed', 'settings-notion-notice-failed', 'Could not connect Notion.'],
] as const) {
  test(`settings: Notion connection result ${result}`, async ({ page }) => {
    await settingsWithNotion(page, result === 'connected' ? notionConnected : notionNotConnected)
    await page.goto(`/settings?notion=${result}`)
    await expect(page.getByText(text)).toBeVisible()
    await page.screenshot(shot(name))
  })
}

const githubRow = (page: Page) => page.getByRole('listitem', { name: 'GitHub' })

test('settings: GitHub status being checked', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await page.route('**/api/v1/integrations/github', () => new Promise(() => {}))
  await page.goto('/settings')
  await expect(githubRow(page).getByText('Checking…')).toBeVisible()
  await page.screenshot(shot('settings-github-checking'))
})

test('settings: GitHub status check failed', async ({ page }) => {
  await mockMe(page, 200, me(true))
  await mockGoogle(page, 200, googleNotConnected)
  await mockGitHub(page, 500, { error: 'internal_error' })
  await page.goto('/settings')
  await expect(githubRow(page).getByRole('button', { name: 'Check GitHub again' })).toBeVisible()
  await page.screenshot(shot('settings-github-check-failed'))
})

test('settings: GitHub not available', async ({ page }) => {
  await settingsWithGitHub(page, { available: false, connected: false, login: '' })
  await page.goto('/settings')
  await expect(githubRow(page).getByText('Not available')).toBeVisible()
  await page.screenshot(shot('settings-github-unavailable'))
})

test('settings: GitHub not connected', async ({ page }) => {
  await settingsWithGitHub(page, githubNotConnected)
  await page.goto('/settings')
  await expect(page.getByRole('button', { name: 'Connect GitHub' })).toBeEnabled()
  await page.screenshot(shot('settings-github-not-connected'))
})

test('settings: connecting GitHub', async ({ page }) => {
  await settingsWithGitHub(page, githubNotConnected)
  // See 'login: redirecting to Slack': a 204 keeps the current document.
  await page.route('**/api/v1/integrations/github/connect', (route) => route.fulfill({ status: 204 }))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Connect GitHub' }).click({ noWaitAfter: true })
  await expect(page.getByRole('button', { name: 'Redirecting to GitHub…' })).toBeDisabled()
  await page.screenshot(shot('settings-github-connecting'))
})

test('settings: GitHub connected', async ({ page }) => {
  await settingsWithGitHub(page, githubConnected)
  await page.goto('/settings')
  await expect(githubRow(page).getByText('Account: @octocat')).toBeVisible()
  await page.screenshot(shot('settings-github-connected'))
})

test('settings: disconnecting GitHub', async ({ page }) => {
  await settingsWithGitHub(page, githubConnected)
  await page.route('**/api/v1/integrations/github/disconnect', () => new Promise(() => {}))
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disconnect GitHub' }).click()
  await expect(page.getByRole('button', { name: 'Disconnecting…' })).toBeDisabled()
  await page.screenshot(shot('settings-github-disconnecting'))
})

test('settings: disconnecting GitHub failed', async ({ page }) => {
  await settingsWithGitHub(page, githubConnected)
  await page.route('**/api/v1/integrations/github/disconnect', (route) =>
    route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"internal_error"}' }),
  )
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disconnect GitHub' }).click()
  await expect(githubRow(page).getByRole('alert')).toContainText('Could not disconnect GitHub.')
  await page.screenshot(shot('settings-github-disconnect-failed'))
})

for (const [result, name, text] of [
  ['connected', 'settings-github-notice-connected', 'GitHub is connected.'],
  ['access_denied', 'settings-github-notice-access-denied', 'you cancelled the request on GitHub'],
  ['account_in_use', 'settings-github-notice-account-in-use', 'already connected to another Robin user'],
  ['failed', 'settings-github-notice-failed', 'Could not connect GitHub.'],
] as const) {
  test(`settings: GitHub connection result ${result}`, async ({ page }) => {
    await settingsWithGitHub(page, result === 'connected' ? githubConnected : githubNotConnected)
    await page.goto(`/settings?github=${result}`)
    await expect(page.getByText(text)).toBeVisible()
    await page.screenshot(shot(name))
  })
}

const scheduled = (page: Page) => page.getByRole('region', { name: 'Scheduled messages' })

// The form starts with the browser's time zone; fix it so every screenshot
// shows the same one.
test.describe('scheduled messages', () => {
  test.use({ timezoneId: 'Asia/Tokyo' })

  test.beforeEach(async ({ page }) => {
    await mockMe(page, 200, me(true))
    await mockGoogle(page, 200, googleNotConnected)
  })

  const posted: LastRun = {
    status: 'succeeded',
    failure: '',
    scheduled_at: '2026-10-04T00:00:00Z',
    deadline: '2026-10-04T00:02:00Z',
    finished_at: '2026-10-04T00:00:12Z',
  }
  const everyLastRun = [
    scheduledJob(1, 'general', null),
    scheduledJob(2, 'team-tokyo', { ...posted, status: 'running', finished_at: null, deadline: '2099-01-01T00:00:00Z' }),
    scheduledJob(3, 'team-osaka', { ...posted, status: 'running', finished_at: null }),
    scheduledJob(4, 'random', posted),
    scheduledJob(5, 'releases', { ...posted, status: 'failed', failure: 'run_failed', finished_at: '2026-10-04T00:00:40Z' }),
    scheduledJob(6, 'support', { ...posted, status: 'skipped', deadline: null, finished_at: '2026-10-04T01:30:00Z' }),
  ]

  test('settings: scheduled messages loading', async ({ page }) => {
    await page.route('**/api/v1/jobs', () => new Promise(() => {}))
    await page.goto('/settings')
    await expect(scheduled(page).getByText('Loading scheduled messages…')).toBeVisible()
    await page.screenshot(shot('settings-jobs-loading'))
  })

  test('settings: scheduled messages could not be loaded', async ({ page }) => {
    await mockJobs(page, 500, { error: 'internal_error' })
    await page.goto('/settings')
    await expect(scheduled(page).getByRole('button', { name: 'Load again' })).toBeVisible()
    await page.screenshot(shot('settings-jobs-load-failed'))
  })

  test('settings: scheduled messages not available', async ({ page }) => {
    await mockJobs(page, 200, jobsStatus([], { available: false, max_jobs: 0 }))
    await page.goto('/settings')
    await expect(scheduled(page).getByText('Your Robin administrator has not set up scheduled messages.')).toBeVisible()
    await page.screenshot(shot('settings-jobs-unavailable'))
  })

  test('settings: no scheduled messages', async ({ page }) => {
    await page.goto('/settings')
    await expect(scheduled(page).getByText('No scheduled messages yet.')).toBeVisible()
    await page.screenshot(shot('settings-jobs-empty'))
  })

  test('settings: scheduled messages with every kind of last run', async ({ page }) => {
    await mockJobs(page, 200, jobsStatus(everyLastRun))
    await page.goto('/settings')
    await expect(page.getByRole('listitem', { name: 'Morning greeting in #support' })).toBeVisible()
    await page.screenshot(shot('settings-jobs-list'))
  })

  test('settings: adding a scheduled message', async ({ page }) => {
    await page.route('**/api/v1/jobs', (route) =>
      route.request().method() === 'POST' ? new Promise(() => {}) : route.fallback(),
    )
    await page.goto('/settings')
    await page.getByLabel('Channel ID').fill('C0GENERAL')
    await page.getByRole('button', { name: 'Add', exact: true }).click()
    await expect(scheduled(page).getByRole('button', { name: 'Adding…' })).toBeDisabled()
    await page.screenshot(shot('settings-jobs-adding'))
  })

  for (const [code, status, name] of [
    ['robin_not_in_channel', 400, 'settings-jobs-add-failed-robin-not-in-channel'],
    ['user_not_in_channel', 400, 'settings-jobs-add-failed-user-not-in-channel'],
    ['channel_not_found', 400, 'settings-jobs-add-failed-channel-not-found'],
    ['channel_archived', 400, 'settings-jobs-add-failed-channel-archived'],
    ['internal_error', 500, 'settings-jobs-add-failed'],
  ] as const) {
    test(`settings: adding a scheduled message rejected with ${code}`, async ({ page }) => {
      await page.route('**/api/v1/jobs', (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ error: code }) })
          : route.fallback(),
      )
      await page.goto('/settings')
      await page.getByLabel('Channel ID').fill('C0GENERAL')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(scheduled(page).getByRole('alert')).toBeVisible()
      await page.screenshot(shot(name))
    })
  }

  test('settings: scheduled messages at the limit', async ({ page }) => {
    await mockJobs(page, 200, jobsStatus(everyLastRun.slice(0, 2), { max_jobs: 2 }))
    await page.goto('/settings')
    await expect(scheduled(page).getByText('You have 2 scheduled messages, the most allowed.', { exact: false })).toBeVisible()
    await page.screenshot(shot('settings-jobs-limit'))
  })

  test('settings: deleting a scheduled message', async ({ page }) => {
    await mockJobs(page, 200, jobsStatus([scheduledJob(1, 'general', posted)]))
    await page.route('**/api/v1/jobs/*', () => new Promise(() => {}))
    await page.goto('/settings')
    await page.getByRole('button', { name: 'Delete Morning greeting in #general' }).click()
    await expect(page.getByRole('button', { name: 'Delete Morning greeting in #general' })).toHaveText('Deleting…')
    await page.screenshot(shot('settings-jobs-deleting'))
  })

  test('settings: deleting a scheduled message failed', async ({ page }) => {
    await mockJobs(page, 200, jobsStatus([scheduledJob(1, 'general', posted)]))
    await page.route('**/api/v1/jobs/*', (route) =>
      route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"internal_error"}' }),
    )
    await page.goto('/settings')
    await page.getByRole('button', { name: 'Delete Morning greeting in #general' }).click()
    await expect(scheduled(page).getByRole('alert')).toContainText('Could not delete this scheduled message.')
    await page.screenshot(shot('settings-jobs-delete-failed'))
  })
})
