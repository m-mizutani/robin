import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../contexts/auth-context'
import Settings from './Settings'

const startLogin = vi.fn()
const startGoogleWorkspaceConnect = vi.fn()
const startNotionConnect = vi.fn()
const startGitHubConnect = vi.fn()
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  startLogin: () => startLogin(),
  startGoogleWorkspaceConnect: () => startGoogleWorkspaceConnect(),
  startNotionConnect: () => startNotionConnect(),
  startGitHubConnect: () => startGitHubConnect(),
}))

function resetStarts() {
  startLogin.mockReset()
  startGoogleWorkspaceConnect.mockReset()
  startNotionConnect.mockReset()
  startGitHubConnect.mockReset()
}

function me(connected: boolean) {
  return JSON.stringify({ team_id: 'T1', user_id: 'U1', name: 'Alice Example', slack_connected: connected })
}

function googleStatus(status: { available: boolean; connected: boolean; email: string }) {
  return JSON.stringify(status)
}

const googleNotConnected = googleStatus({ available: true, connected: false, email: '' })
const googleConnected = googleStatus({ available: true, connected: true, email: 'alice@example.com' })

const googlePath = '/api/v1/integrations/google-workspace'

function notionStatus(status: {
  available?: boolean
  connected?: boolean
  needs_reconnect?: boolean
  user_name?: string
  workspace_name?: string
}) {
  return JSON.stringify({
    available: true,
    connected: false,
    needs_reconnect: false,
    user_name: '',
    workspace_name: '',
    ...status,
  })
}

const notionNotConnected = notionStatus({})
const notionConnected = notionStatus({ connected: true, user_name: 'Alice Example', workspace_name: 'Acme' })

const notionPath = '/api/v1/integrations/notion'

function githubStatus(status: { available: boolean; connected: boolean; login: string }) {
  return JSON.stringify(status)
}

const githubNotConnected = githubStatus({ available: true, connected: false, login: '' })
const githubConnected = githubStatus({ available: true, connected: true, login: 'octocat' })

const githubPath = '/api/v1/integrations/github'

const jobsPath = '/api/v1/jobs'
const noJobs = '{"setting":null,"triggers":[]}'

// Shows the current URL, so tests can check that the result parameter is
// removed after the notice is shown.
function CurrentLocation() {
  const location = useLocation()
  return <p data-testid="location">{location.pathname + location.search}</p>
}

function renderSettings(path = '/settings') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <Routes>
          <Route
            path="/settings"
            element={
              <>
                <Settings />
                <CurrentLocation />
              </>
            }
          />
          <Route path="/login" element={<p>login page</p>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
}

type Handler = (url: string, init?: RequestInit) => Promise<Response>

// stubFetch answers every request with handler, except the list of scheduled
// messages, which is empty: the tests of this file are about the rest of the
// page, and components/ScheduledMessages.test.tsx covers that section.
function stubFetch(handler: Handler) {
  const mock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === jobsPath && (init?.method ?? 'GET') === 'GET') {
      return Promise.resolve(new Response(noJobs, { status: 200 }))
    }
    return handler(url, init)
  })
  vi.stubGlobal('fetch', mock)
  return mock
}

// Answers /api/v1/auth/me with meBody, the Google Workspace status with
// googleBody, the Notion status with notionBody, and the GitHub status with
// githubBody; other paths go to rest.
function stubApi(
  meBody: string,
  googleBody: string = googleNotConnected,
  rest?: Handler,
  notionBody = notionNotConnected,
  githubBody = githubNotConnected,
) {
  return stubFetch(async (url, init) => {
    if (url === '/api/v1/auth/me') {
      return new Response(meBody, { status: 200 })
    }
    if (url === googlePath) {
      return new Response(googleBody, { status: 200 })
    }
    if (url === notionPath) {
      return new Response(notionBody, { status: 200 })
    }
    if (url === githubPath) {
      return new Response(githubBody, { status: 200 })
    }
    if (rest) {
      return rest(url, init)
    }
    return new Response('{"error":"not_found"}', { status: 404 })
  })
}

function callsTo(mock: ReturnType<typeof stubFetch>, url: string, method = 'GET') {
  return mock.mock.calls.filter(([input, init]) => String(input) === url && (init?.method ?? 'GET') === method)
}

async function row(name: string) {
  return within(await screen.findByRole('listitem', { name }))
}

describe('Settings', () => {
  beforeEach(resetStarts)

  it('shows a linked Slack account without a way to disconnect it', async () => {
    stubApi(me(true))
    renderSettings()

    expect(await screen.findByRole('heading', { name: 'Settings', level: 1 })).toBeInTheDocument()
    expect(screen.getByText('Signed in as Alice Example')).toBeInTheDocument()
    const slack = await row('Slack')
    expect(slack.getByText('Connected')).toBeInTheDocument()
    expect(slack.getByText(/you cannot disconnect Slack on this page/)).toBeInTheDocument()
    expect(slack.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Disconnect Slack' })).toBeNull()
  })

  it('shows scheduled messages above the integrations', async () => {
    stubApi(me(true))
    renderSettings()

    expect(await screen.findByLabelText('Channel ID')).toBeInTheDocument()
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'Scheduled messages',
      'Integrations',
    ])
  })

  it('offers to connect Slack when the account is not linked', async () => {
    stubApi(me(false))
    renderSettings()

    const slack = await row('Slack')
    expect(slack.getByText('Not connected')).toBeInTheDocument()
    expect(slack.getByRole('button', { name: 'Connect Slack' })).toBeEnabled()
  })

  it('starts the Slack authorization and disables the button', async () => {
    stubApi(me(false))
    renderSettings()

    fireEvent.click((await row('Slack')).getByRole('button', { name: 'Connect Slack' }))

    expect(startLogin).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Redirecting to Slack…' })).toBeDisabled()
  })

  it('fetches the user, the scheduled messages and the status of each integration once', async () => {
    const fetchMock = stubApi(me(true))
    renderSettings()

    await screen.findByRole('heading', { name: 'Integrations' })
    expect(screen.queryByText('Coming soon')).toBeNull()
    // /auth/me, the scheduled messages, and the status of Google Workspace,
    // Notion, and GitHub; nothing else.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5))
    expect(callsTo(fetchMock, jobsPath)).toHaveLength(1)
  })

  it('lists the services in display order', async () => {
    stubApi(me(true))
    renderSettings()

    await screen.findByRole('heading', { name: 'Integrations' })
    expect(screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual([
      'Slack',
      'Google Workspace',
      'Notion',
      'GitHub',
    ])
  })

  it('signs out and moves to the login page', async () => {
    let signedOut = false
    const fetchMock = stubFetch(async (url, init) => {
      if (url === '/api/v1/auth/logout' && init?.method === 'POST') {
        signedOut = true
        return new Response('{"success":true}', { status: 200 })
      }
      if (url === googlePath) {
        return new Response(googleNotConnected, { status: 200 })
      }
      if (url === notionPath) {
        return new Response(notionNotConnected, { status: 200 })
      }
      return signedOut
        ? new Response('{"error":"unauthenticated"}', { status: 401 })
        : new Response(me(true), { status: 200 })
    })
    renderSettings()

    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }))

    expect(await screen.findByText('login page')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/logout', expect.objectContaining({ method: 'POST' }))
  })

  it('reports a failed sign-out and enables the button again', async () => {
    stubApi(me(true), googleNotConnected, async () => new Response('{"error":"internal_error"}', { status: 500 }))
    renderSettings()

    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not sign out. Try again.')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Sign out' })).toBeEnabled())
  })
})

describe('Settings: Google Workspace', () => {
  beforeEach(resetStarts)

  it('shows that the status is being checked', async () => {
    stubFetch(async (url) => {
      if (url === googlePath) {
        return new Promise<Response>(() => {})
      }
      return new Response(url === notionPath ? notionNotConnected : me(true), { status: 200 })
    })
    renderSettings()

    const google = await row('Google Workspace')
    expect(google.getByText('Checking…')).toBeInTheDocument()
    expect(google.queryByRole('button')).toBeNull()
  })

  it('reports a failed status check and checks again', async () => {
    let failing = true
    const fetchMock = stubFetch(async (url) => {
      if (url === googlePath) {
        return failing
          ? new Response('{"error":"internal_error"}', { status: 500 })
          : new Response(googleNotConnected, { status: 200 })
      }
      return new Response(url === notionPath ? notionNotConnected : me(true), { status: 200 })
    })
    renderSettings()

    const google = await row('Google Workspace')
    expect(await google.findByText('Could not load the connection status.')).toBeInTheDocument()

    failing = false
    fireEvent.click(google.getByRole('button', { name: 'Check Google Workspace again' }))

    expect(await google.findByText('Not connected')).toBeInTheDocument()
    expect(callsTo(fetchMock, googlePath)).toHaveLength(2)
  })

  it('shows that the server has not set up the integration', async () => {
    stubApi(me(true), googleStatus({ available: false, connected: false, email: '' }))
    renderSettings()

    const google = await row('Google Workspace')
    expect(await google.findByText('Not available')).toBeInTheDocument()
    expect(google.getByText('Your Robin administrator has not set up this integration.')).toBeInTheDocument()
    expect(google.queryByRole('button')).toBeNull()
  })

  it('starts the Google authorization and disables the button', async () => {
    stubApi(me(true))
    renderSettings()

    const google = await row('Google Workspace')
    expect(await google.findByText('Not connected')).toBeInTheDocument()
    expect(google.getByText(/read-only access to your Google Calendar, Gmail, and Drive files/)).toBeInTheDocument()

    fireEvent.click(google.getByRole('button', { name: 'Connect Google Workspace' }))

    expect(startGoogleWorkspaceConnect).toHaveBeenCalledTimes(1)
    expect(startLogin).not.toHaveBeenCalled()
    expect(google.getByRole('button', { name: 'Redirecting to Google Workspace…' })).toBeDisabled()
  })

  it('shows the connected account with a way to disconnect it', async () => {
    stubApi(me(true), googleConnected)
    renderSettings()

    const google = await row('Google Workspace')
    expect(await google.findByText('Connected')).toBeInTheDocument()
    expect(google.getByText('Account: alice@example.com')).toBeInTheDocument()
    expect(google.getByRole('button', { name: 'Disconnect Google Workspace' })).toBeEnabled()
    expect((await row('Slack')).queryByRole('button')).toBeNull()
  })

  it('disconnects and shows the account as not connected', async () => {
    let connected = true
    let release: () => void = () => {}
    const fetchMock = stubFetch(async (url, init) => {
      if (url === `${googlePath}/disconnect` && init?.method === 'POST') {
        await new Promise<void>((resolve) => {
          release = resolve
        })
        connected = false
        return new Response('{"success":true}', { status: 200 })
      }
      if (url === googlePath) {
        return new Response(connected ? googleConnected : googleNotConnected, { status: 200 })
      }
      return new Response(url === notionPath ? notionNotConnected : me(true), { status: 200 })
    })
    renderSettings()

    const google = await row('Google Workspace')
    fireEvent.click(await google.findByRole('button', { name: 'Disconnect Google Workspace' }))

    expect(await google.findByRole('button', { name: 'Disconnecting…' })).toBeDisabled()
    release()

    expect(await google.findByText('Not connected')).toBeInTheDocument()
    expect(google.getByRole('button', { name: 'Connect Google Workspace' })).toBeEnabled()
    expect(google.queryByText(/Account:/)).toBeNull()
    expect(callsTo(fetchMock, `${googlePath}/disconnect`, 'POST')).toHaveLength(1)
    expect(callsTo(fetchMock, googlePath)).toHaveLength(2)
    expect(callsTo(fetchMock, notionPath)).toHaveLength(1)
  })

  it('reports a failed disconnection and keeps the account connected', async () => {
    stubApi(me(true), googleConnected, async () => new Response('{"error":"internal_error"}', { status: 500 }))
    renderSettings()

    const google = await row('Google Workspace')
    fireEvent.click(await google.findByRole('button', { name: 'Disconnect Google Workspace' }))

    expect(await google.findByRole('alert')).toHaveTextContent('Could not disconnect Google Workspace. Try again.')
    expect(google.getByText('Connected')).toBeInTheDocument()
    await waitFor(() => expect(google.getByRole('button', { name: 'Disconnect Google Workspace' })).toBeEnabled())
  })

  it.each([
    ['connected', 'status', 'Google Workspace is connected.'],
    ['access_denied', 'alert', 'Google Workspace was not connected because you cancelled the request on Google.'],
    [
      'missing_scope',
      'alert',
      'Google Workspace was not connected because you did not allow every requested permission. Connect again and allow all of them.',
    ],
    [
      'account_in_use',
      'alert',
      'Google Workspace was not connected because this Google account is already connected to another Robin user. Connect a different Google account.',
    ],
    ['failed', 'alert', 'Could not connect Google Workspace. Try again.'],
  ])('shows the result %s and removes it from the URL', async (result, role, text) => {
    stubApi(me(true), result === 'connected' ? googleConnected : googleNotConnected)
    renderSettings(`/settings?google_workspace=${result}`)

    expect(await screen.findByRole(role, { name: '' })).toHaveTextContent(text)
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent(/^\/settings$/))
  })

  it('ignores an unknown result', async () => {
    stubApi(me(true))
    renderSettings('/settings?google_workspace=unknown')

    await screen.findByRole('heading', { name: 'Integrations' })
    expect(screen.queryByRole('status')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

// Answers the Notion status from notion(), the Google Workspace status as not
// connected, /api/v1/auth/me as a linked user, and other paths with rest.
function stubNotion(notion: () => Promise<Response>, rest?: Handler) {
  return stubFetch(async (url, init) => {
    if (url === notionPath) {
      return notion()
    }
    if (url === googlePath) {
      return new Response(googleNotConnected, { status: 200 })
    }
    if (url === '/api/v1/auth/me') {
      return new Response(me(true), { status: 200 })
    }
    if (rest) {
      return rest(url, init)
    }
    return new Response('{"error":"not_found"}', { status: 404 })
  })
}

const ok = (body: string) => async () => new Response(body, { status: 200 })

describe('Settings: Notion', () => {
  beforeEach(resetStarts)

  it('shows that the status is being checked', async () => {
    stubNotion(() => new Promise<Response>(() => {}))
    renderSettings()

    const notion = await row('Notion')
    expect(notion.getByText('Checking…')).toBeInTheDocument()
    expect(notion.queryByRole('button')).toBeNull()
  })

  it('reports a failed status check and checks only Notion again', async () => {
    let failing = true
    const fetchMock = stubNotion(async () =>
      failing
        ? new Response('{"error":"internal_error"}', { status: 500 })
        : new Response(notionNotConnected, { status: 200 }),
    )
    renderSettings()

    const notion = await row('Notion')
    expect(await notion.findByText('Could not load the connection status.')).toBeInTheDocument()

    failing = false
    fireEvent.click(notion.getByRole('button', { name: 'Check Notion again' }))

    expect(await notion.findByText('Not connected')).toBeInTheDocument()
    expect(callsTo(fetchMock, notionPath)).toHaveLength(2)
    expect(callsTo(fetchMock, googlePath)).toHaveLength(1)
  })

  it('shows that the server has not set up the integration', async () => {
    stubNotion(ok(notionStatus({ available: false })))
    renderSettings()

    const notion = await row('Notion')
    expect(await notion.findByText('Not available')).toBeInTheDocument()
    expect(notion.getByText('Your Robin administrator has not set up this integration.')).toBeInTheDocument()
    expect(notion.queryByRole('button')).toBeNull()
  })

  it('starts the Notion authorization and disables the button', async () => {
    stubNotion(ok(notionNotConnected))
    renderSettings()

    const notion = await row('Notion')
    expect(await notion.findByText('Not connected')).toBeInTheDocument()
    expect(
      notion.getByText(
        'Gives Robin read-only access to the Notion pages and databases you share with it. No Robin feature uses this access yet. Robin cannot create or change anything.',
      ),
    ).toBeInTheDocument()

    fireEvent.click(notion.getByRole('button', { name: 'Connect Notion' }))

    expect(startNotionConnect).toHaveBeenCalledTimes(1)
    expect(startGoogleWorkspaceConnect).not.toHaveBeenCalled()
    expect(notion.getByRole('button', { name: 'Redirecting to Notion…' })).toBeDisabled()
  })

  it('shows the connected account with a way to disconnect it', async () => {
    stubNotion(ok(notionConnected))
    renderSettings()

    const notion = await row('Notion')
    expect(await notion.findByText('Connected')).toBeInTheDocument()
    expect(notion.getByText('Account: Alice Example (Acme)')).toBeInTheDocument()
    expect(notion.getByRole('button', { name: 'Disconnect Notion' })).toBeEnabled()
    expect(notion.queryByRole('button', { name: 'Connect Notion' })).toBeNull()
  })

  it('disconnects without a confirmation and refreshes only Notion', async () => {
    let connected = true
    let release: () => void = () => {}
    const fetchMock = stubNotion(
      async () => new Response(connected ? notionConnected : notionNotConnected, { status: 200 }),
      async (url, init) => {
        if (url === `${notionPath}/disconnect` && init?.method === 'POST') {
          await new Promise<void>((resolve) => {
            release = resolve
          })
          connected = false
          return new Response('{"success":true}', { status: 200 })
        }
        return new Response('{"error":"not_found"}', { status: 404 })
      },
    )
    renderSettings()

    const notion = await row('Notion')
    fireEvent.click(await notion.findByRole('button', { name: 'Disconnect Notion' }))

    expect(await notion.findByRole('button', { name: 'Disconnecting…' })).toBeDisabled()
    release()

    expect(await notion.findByText('Not connected')).toBeInTheDocument()
    expect(notion.getByRole('button', { name: 'Connect Notion' })).toBeEnabled()
    expect(notion.queryByText(/Account:/)).toBeNull()
    expect(callsTo(fetchMock, `${notionPath}/disconnect`, 'POST')).toHaveLength(1)
    expect(callsTo(fetchMock, notionPath)).toHaveLength(2)
    expect(callsTo(fetchMock, googlePath)).toHaveLength(1)
  })

  it('reports a failed disconnection and keeps the account connected', async () => {
    stubNotion(ok(notionConnected), async () => new Response('{"error":"internal_error"}', { status: 500 }))
    renderSettings()

    const notion = await row('Notion')
    fireEvent.click(await notion.findByRole('button', { name: 'Disconnect Notion' }))

    expect(await notion.findByRole('alert')).toHaveTextContent('Could not disconnect Notion. Try again.')
    expect(notion.getByText('Connected')).toBeInTheDocument()
    await waitFor(() => expect(notion.getByRole('button', { name: 'Disconnect Notion' })).toBeEnabled())
  })

  it('asks to reconnect when Robin can no longer access Notion', async () => {
    stubNotion(ok(notionStatus({ connected: true, needs_reconnect: true, user_name: 'Alice Example', workspace_name: 'Acme' })))
    renderSettings()

    const notion = await row('Notion')
    expect(await notion.findByText('Reconnect required')).toBeInTheDocument()
    expect(notion.getByText('Account: Alice Example (Acme)')).toBeInTheDocument()
    expect(
      notion.getByText('Robin can no longer access your Notion pages. Reconnect Notion to give Robin access again.'),
    ).toBeInTheDocument()
    expect(notion.getByRole('button', { name: 'Disconnect Notion' })).toBeEnabled()

    fireEvent.click(notion.getByRole('button', { name: 'Reconnect Notion' }))
    expect(startNotionConnect).toHaveBeenCalledTimes(1)
    expect(notion.getByRole('button', { name: 'Redirecting to Notion…' })).toBeDisabled()
  })

  it.each([
    ['connected', 'status', 'Notion is connected.'],
    ['access_denied', 'alert', 'Notion was not connected because you cancelled the request on Notion.'],
    [
      'wrong_workspace',
      'alert',
      'Notion was not connected because the workspace you chose is not the one Robin is set up for. Ask your Robin administrator which workspace to use.',
    ],
    [
      'account_in_use',
      'alert',
      'Notion was not connected because this Notion account is already connected to another Robin user. Connect a different Notion account.',
    ],
    ['failed', 'alert', 'Could not connect Notion. Try again.'],
  ])('shows the result %s and removes it from the URL', async (result, role, text) => {
    stubNotion(ok(result === 'connected' ? notionConnected : notionNotConnected))
    renderSettings(`/settings?notion=${result}`)

    expect(await screen.findByRole(role, { name: '' })).toHaveTextContent(text)
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent(/^\/settings$/))
  })

  it('ignores an unknown result', async () => {
    stubNotion(ok(notionNotConnected))
    renderSettings('/settings?notion=unknown')

    await screen.findByRole('heading', { name: 'Integrations' })
    expect(screen.queryByRole('status')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

// Answers every status API with a not-connected account, except GitHub, which
// github answers.
function stubGitHub(github: Handler) {
  return stubFetch(async (url, init) => {
    if (url === githubPath || url.startsWith(`${githubPath}/`)) {
      return github(url, init)
    }
    if (url === googlePath) {
      return new Response(googleNotConnected, { status: 200 })
    }
    if (url === notionPath) {
      return new Response(notionNotConnected, { status: 200 })
    }
    return new Response(me(true), { status: 200 })
  })
}

describe('Settings: GitHub', () => {
  beforeEach(resetStarts)

  it('shows that the status is being checked', async () => {
    stubGitHub(() => new Promise<Response>(() => {}))
    renderSettings()

    const github = await row('GitHub')
    expect(github.getByText('Checking…')).toBeInTheDocument()
    expect(github.queryByRole('button')).toBeNull()
  })

  it('reports a failed status check and checks only GitHub again', async () => {
    let failing = true
    const fetchMock = stubGitHub(async () =>
      failing
        ? new Response('{"error":"internal_error"}', { status: 500 })
        : new Response(githubNotConnected, { status: 200 }),
    )
    renderSettings()

    const github = await row('GitHub')
    expect(await github.findByText('Could not load the connection status.')).toBeInTheDocument()

    failing = false
    fireEvent.click(github.getByRole('button', { name: 'Check GitHub again' }))

    expect(await github.findByText('Not connected')).toBeInTheDocument()
    expect(callsTo(fetchMock, githubPath)).toHaveLength(2)
    expect(callsTo(fetchMock, googlePath)).toHaveLength(1)
    expect(callsTo(fetchMock, notionPath)).toHaveLength(1)
  })

  it('shows that the server has not set up the integration', async () => {
    stubApi(
      me(true),
      googleNotConnected,
      undefined,
      notionNotConnected,
      githubStatus({ available: false, connected: false, login: '' }),
    )
    renderSettings()

    const github = await row('GitHub')
    expect(await github.findByText('Not available')).toBeInTheDocument()
    expect(github.getByText('Your Robin administrator has not set up this integration.')).toBeInTheDocument()
    expect(github.queryByRole('button')).toBeNull()
  })

  it('starts the GitHub authorization and disables the button', async () => {
    stubApi(me(true))
    renderSettings()

    const github = await row('GitHub')
    expect(await github.findByText('Not connected')).toBeInTheDocument()
    expect(github.getByText(/read-only access to the repositories, issues, pull requests/)).toBeInTheDocument()

    fireEvent.click(github.getByRole('button', { name: 'Connect GitHub' }))

    expect(startGitHubConnect).toHaveBeenCalledTimes(1)
    expect(startLogin).not.toHaveBeenCalled()
    expect(startGoogleWorkspaceConnect).not.toHaveBeenCalled()
    expect(startNotionConnect).not.toHaveBeenCalled()
    expect(github.getByRole('button', { name: 'Redirecting to GitHub…' })).toBeDisabled()
  })

  it('shows the connected account with a way to disconnect it', async () => {
    stubApi(me(true), googleNotConnected, undefined, notionNotConnected, githubConnected)
    renderSettings()

    const github = await row('GitHub')
    expect(await github.findByText('Connected')).toBeInTheDocument()
    expect(github.getByText('Account: @octocat')).toBeInTheDocument()
    expect(github.getByRole('button', { name: 'Disconnect GitHub' })).toBeEnabled()
  })

  it('disconnects and checks only GitHub again', async () => {
    let connected = true
    let release: () => void = () => {}
    const fetchMock = stubGitHub(async (url, init) => {
      if (url === `${githubPath}/disconnect` && init?.method === 'POST') {
        await new Promise<void>((resolve) => {
          release = resolve
        })
        connected = false
        return new Response('{"success":true}', { status: 200 })
      }
      return new Response(connected ? githubConnected : githubNotConnected, { status: 200 })
    })
    renderSettings()

    const github = await row('GitHub')
    fireEvent.click(await github.findByRole('button', { name: 'Disconnect GitHub' }))

    expect(await github.findByRole('button', { name: 'Disconnecting…' })).toBeDisabled()
    release()

    expect(await github.findByText('Not connected')).toBeInTheDocument()
    expect(github.getByRole('button', { name: 'Connect GitHub' })).toBeEnabled()
    expect(github.queryByText(/Account:/)).toBeNull()
    expect(callsTo(fetchMock, `${githubPath}/disconnect`, 'POST')).toHaveLength(1)
    expect(callsTo(fetchMock, githubPath)).toHaveLength(2)
    expect(callsTo(fetchMock, googlePath)).toHaveLength(1)
    expect(callsTo(fetchMock, notionPath)).toHaveLength(1)
  })

  it('reports a failed disconnection and keeps the account connected', async () => {
    stubGitHub(async (url, init) =>
      url === `${githubPath}/disconnect` && init?.method === 'POST'
        ? new Response('{"error":"internal_error"}', { status: 500 })
        : new Response(githubConnected, { status: 200 }),
    )
    renderSettings()

    const github = await row('GitHub')
    fireEvent.click(await github.findByRole('button', { name: 'Disconnect GitHub' }))

    expect(await github.findByRole('alert')).toHaveTextContent('Could not disconnect GitHub. Try again.')
    expect(github.getByText('Connected')).toBeInTheDocument()
    await waitFor(() => expect(github.getByRole('button', { name: 'Disconnect GitHub' })).toBeEnabled())
  })

  it.each([
    ['connected', 'status', 'GitHub is connected.'],
    ['access_denied', 'alert', 'GitHub was not connected because you cancelled the request on GitHub.'],
    [
      'account_in_use',
      'alert',
      'GitHub was not connected because this GitHub account is already connected to another Robin user. Connect a different GitHub account.',
    ],
    ['failed', 'alert', 'Could not connect GitHub. Try again.'],
  ])('shows the result %s and removes it from the URL', async (result, role, text) => {
    stubApi(
      me(true),
      googleNotConnected,
      undefined,
      notionNotConnected,
      result === 'connected' ? githubConnected : githubNotConnected,
    )
    renderSettings(`/settings?github=${result}`)

    expect(await screen.findByRole(role, { name: '' })).toHaveTextContent(text)
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent(/^\/settings$/))
  })

  it('ignores an unknown result', async () => {
    stubApi(me(true))
    renderSettings('/settings?github=missing_scope')

    await screen.findByRole('heading', { name: 'Integrations' })
    expect(screen.queryByRole('status')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
