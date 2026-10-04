import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import {
  disconnectGitHub,
  disconnectGoogleWorkspace,
  disconnectNotion,
  fetchGitHubStatus,
  fetchGoogleWorkspaceStatus,
  fetchNotionStatus,
  logout,
  startGitHubConnect,
  startGoogleWorkspaceConnect,
  startLogin,
  startNotionConnect,
} from '../api'
import ScheduledMessages from '../components/ScheduledMessages'
import { useAuth } from '../contexts/auth-context'
import {
  listIntegrations,
  type Integration,
  type IntegrationID,
  type IntegrationStatus,
  type ServiceState,
} from '../integrations'

// Services whose connection can be started now. Every other service shows a
// disabled button.
const connectActions: Partial<Record<IntegrationID, () => void>> = {
  slack: () => startLogin(),
  google_workspace: () => startGoogleWorkspaceConnect(),
  notion: () => startNotionConnect(),
  github: () => startGitHubConnect(),
}

// Services that can be disconnected on this page. Slack is the sign-in method,
// so it is not here.
const disconnectActions: Partial<Record<IntegrationID, () => Promise<void>>> = {
  google_workspace: disconnectGoogleWorkspace,
  notion: disconnectNotion,
  github: disconnectGitHub,
}

const statusLabels: Record<IntegrationStatus, { text: string; className: string }> = {
  connected: { text: 'Connected', className: 'success' },
  not_connected: { text: 'Not connected', className: 'error' },
  coming_soon: { text: 'Coming soon', className: 'muted' },
  unavailable: { text: 'Not available', className: 'muted' },
  checking: { text: 'Checking…', className: 'muted' },
  check_failed: { text: 'Could not load the connection status.', className: 'error' },
  needs_reconnect: { text: 'Reconnect required', className: 'error' },
}

type Notice = { text: string; className: 'success' | 'error'; role: 'status' | 'alert' }

// After a connection attempt the server returns to /settings with one of
// these parameters, whose value names the result. Any other value is ignored.
const resultNotices: Record<string, Record<string, Notice>> = {
  google_workspace: {
    connected: { text: 'Google Workspace is connected.', className: 'success', role: 'status' },
    access_denied: {
      text: 'Google Workspace was not connected because you cancelled the request on Google.',
      className: 'error',
      role: 'alert',
    },
    missing_scope: {
      text: 'Google Workspace was not connected because you did not allow every requested permission. Connect again and allow all of them.',
      className: 'error',
      role: 'alert',
    },
    account_in_use: {
      text: 'Google Workspace was not connected because this Google account is already connected to another Robin user. Connect a different Google account.',
      className: 'error',
      role: 'alert',
    },
    failed: { text: 'Could not connect Google Workspace. Try again.', className: 'error', role: 'alert' },
  },
  notion: {
    connected: { text: 'Notion is connected.', className: 'success', role: 'status' },
    access_denied: {
      text: 'Notion was not connected because you cancelled the request on Notion.',
      className: 'error',
      role: 'alert',
    },
    wrong_workspace: {
      text: 'Notion was not connected because the workspace you chose is not the one Robin is set up for. Ask your Robin administrator which workspace to use.',
      className: 'error',
      role: 'alert',
    },
    account_in_use: {
      text: 'Notion was not connected because this Notion account is already connected to another Robin user. Connect a different Notion account.',
      className: 'error',
      role: 'alert',
    },
    failed: { text: 'Could not connect Notion. Try again.', className: 'error', role: 'alert' },
  },
  github: {
    connected: { text: 'GitHub is connected.', className: 'success', role: 'status' },
    access_denied: {
      text: 'GitHub was not connected because you cancelled the request on GitHub.',
      className: 'error',
      role: 'alert',
    },
    account_in_use: {
      text: 'GitHub was not connected because this GitHub account is already connected to another Robin user. Connect a different GitHub account.',
      className: 'error',
      role: 'alert',
    },
    failed: { text: 'Could not connect GitHub. Try again.', className: 'error', role: 'alert' },
  },
}

function noticeFor(searchParams: URLSearchParams): Notice | null {
  for (const [param, notices] of Object.entries(resultNotices)) {
    const result = searchParams.get(param)
    if (result !== null && Object.prototype.hasOwnProperty.call(notices, result)) {
      return notices[result]
    }
  }
  return null
}

// useServiceStatus fetches the status of one service when the page opens and
// returns the state with a function that fetches it again.
function useServiceStatus<T>(fetchStatus: () => Promise<T>) {
  const [state, setState] = useState<ServiceState<T>>({ kind: 'loading' })
  const load = useCallback(async () => {
    setState({ kind: 'loading' })
    try {
      setState({ kind: 'loaded', status: await fetchStatus() })
    } catch {
      setState({ kind: 'error' })
    }
  }, [fetchStatus])
  useEffect(() => {
    void load()
  }, [load])
  return [state, load] as const
}

export default function Settings() {
  const { state, reload } = useAuth()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [connecting, setConnecting] = useState<IntegrationID | null>(null)
  const [disconnecting, setDisconnecting] = useState<IntegrationID | null>(null)
  const [disconnectFailed, setDisconnectFailed] = useState<IntegrationID | null>(null)
  const [signingOut, setSigningOut] = useState(false)
  const [signOutFailed, setSignOutFailed] = useState(false)
  const [google, loadGoogle] = useServiceStatus(fetchGoogleWorkspaceStatus)
  const [notion, loadNotion] = useServiceStatus(fetchNotionStatus)
  const [github, loadGitHub] = useServiceStatus(fetchGitHubStatus)
  const [notice] = useState(() => noticeFor(searchParams))

  // Services whose status the page fetches from their own API.
  const reloadActions = useMemo<Partial<Record<IntegrationID, () => Promise<void>>>>(
    () => ({ google_workspace: loadGoogle, notion: loadNotion, github: loadGitHub }),
    [loadGoogle, loadNotion, loadGitHub],
  )

  // Remove the result from the URL once it is shown, so a reload does not
  // show it again.
  useEffect(() => {
    if (Object.keys(resultNotices).some((param) => searchParams.has(param))) {
      setSearchParams({}, { replace: true })
    }
  }, [searchParams, setSearchParams])

  if (state.kind !== 'authenticated') {
    return null
  }
  const { me } = state

  const onConnect = (id: IntegrationID) => {
    const action = connectActions[id]
    if (!action) {
      return
    }
    setConnecting(id)
    action()
  }

  const onDisconnect = async (id: IntegrationID) => {
    const action = disconnectActions[id]
    if (!action) {
      return
    }
    setDisconnecting(id)
    setDisconnectFailed(null)
    try {
      await action()
    } catch {
      setDisconnectFailed(id)
      setDisconnecting(null)
      return
    }
    setDisconnecting(null)
    await reloadActions[id]?.()
  }

  const onSignOut = async () => {
    setSigningOut(true)
    setSignOutFailed(false)
    try {
      await logout()
    } catch {
      setSignOutFailed(true)
      setSigningOut(false)
      return
    }
    await reload()
    navigate('/login', { replace: true })
  }

  const renderConnect = (integration: Integration, label: string) => {
    const isConnecting = connecting === integration.id
    const canConnect = integration.status !== 'coming_soon' && connectActions[integration.id] !== undefined
    return (
      <button
        type="button"
        className="button"
        onClick={() => onConnect(integration.id)}
        disabled={!canConnect || isConnecting}
      >
        {isConnecting ? `Redirecting to ${integration.name}…` : label}
      </button>
    )
  }

  const renderDisconnect = (integration: Integration) => {
    if (!disconnectActions[integration.id]) {
      return null
    }
    const isDisconnecting = disconnecting === integration.id
    return (
      <button
        type="button"
        className="button secondary"
        onClick={() => void onDisconnect(integration.id)}
        disabled={isDisconnecting}
      >
        {isDisconnecting ? 'Disconnecting…' : `Disconnect ${integration.name}`}
      </button>
    )
  }

  const renderAction = (integration: Integration) => {
    switch (integration.status) {
      case 'connected':
        return renderDisconnect(integration)
      case 'needs_reconnect':
        return (
          <>
            {renderConnect(integration, `Reconnect ${integration.name}`)}
            {renderDisconnect(integration)}
          </>
        )
      case 'check_failed': {
        const recheck = reloadActions[integration.id]
        return (
          <button type="button" className="button secondary" onClick={() => void recheck?.()}>
            Check {integration.name} again
          </button>
        )
      }
      case 'checking':
      case 'unavailable':
        return null
      case 'not_connected':
      case 'coming_soon':
        return renderConnect(integration, `Connect ${integration.name}`)
    }
  }

  return (
    <main className="page">
      <section className="card">
        <h1 className="title">Settings</h1>
        <p className="muted">Signed in as {me.name}</p>
        <ScheduledMessages />
        <h2>Integrations</h2>
        {notice && (
          <p className={notice.className} role={notice.role}>
            {notice.text}
          </p>
        )}
        <ul className="integration-list">
          {listIntegrations(me, google, notion, github).map((integration) => {
            const label = statusLabels[integration.status]
            return (
              <li key={integration.id} className="integration" aria-labelledby={`integration-${integration.id}`}>
                <h3 id={`integration-${integration.id}`}>{integration.name}</h3>
                <p className={label.className}>{label.text}</p>
                {integration.account && <p className="muted">Account: {integration.account}</p>}
                <p className="muted">{integration.description}</p>
                {disconnectFailed === integration.id && (
                  <p className="error" role="alert">
                    Could not disconnect {integration.name}. Try again.
                  </p>
                )}
                {renderAction(integration)}
              </li>
            )
          })}
        </ul>
        {signOutFailed && (
          <p className="error" role="alert">
            Could not sign out. Try again.
          </p>
        )}
        <button type="button" className="button secondary" onClick={() => void onSignOut()} disabled={signingOut}>
          {signingOut ? 'Signing out…' : 'Sign out'}
        </button>
      </section>
    </main>
  )
}
