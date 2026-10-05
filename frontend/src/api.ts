export type Me = {
  team_id: string
  user_id: string
  name: string
  slack_connected: boolean
}

export type MeResult =
  | { kind: 'authenticated'; me: Me }
  | { kind: 'unauthenticated' }
  | { kind: 'error'; message: string }

// Every API route of the server is under this prefix.
const apiV1 = '/api/v1'
const authPath = `${apiV1}/auth`

export async function fetchMe(): Promise<MeResult> {
  try {
    const res = await fetch(`${authPath}/me`, { credentials: 'include' })
    if (res.status === 401) {
      return { kind: 'unauthenticated' }
    }
    if (!res.ok) {
      return { kind: 'error', message: `HTTP ${res.status}` }
    }
    return { kind: 'authenticated', me: (await res.json()) as Me }
  } catch (e) {
    return { kind: 'error', message: e instanceof Error ? e.message : String(e) }
  }
}

export async function logout(): Promise<void> {
  const res = await fetch(`${authPath}/logout`, { method: 'POST', credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
}

export function startLogin(): void {
  window.location.assign(`${authPath}/login`)
}

export type GoogleWorkspaceStatus = {
  // false when the server has no Google OAuth client configured
  available: boolean
  connected: boolean
  // the connected Google account; empty when not connected
  email: string
}

const googleWorkspacePath = `${apiV1}/integrations/google-workspace`

// Throws when the request fails or the response is not 2xx.
export async function fetchGoogleWorkspaceStatus(): Promise<GoogleWorkspaceStatus> {
  const res = await fetch(googleWorkspacePath, { credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
  return (await res.json()) as GoogleWorkspaceStatus
}

export async function disconnectGoogleWorkspace(): Promise<void> {
  const res = await fetch(`${googleWorkspacePath}/disconnect`, { method: 'POST', credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
}

export function startGoogleWorkspaceConnect(): void {
  window.location.assign(`${googleWorkspacePath}/connect`)
}

export type NotionStatus = {
  // false when the server has no Notion integration configured
  available: boolean
  connected: boolean
  // true when Notion stopped accepting the stored authorization
  needs_reconnect: boolean
  // the Notion user and workspace of the connection; empty when not connected
  user_name: string
  workspace_name: string
}

const notionPath = `${apiV1}/integrations/notion`

// Throws when the request fails or the response is not 2xx.
export async function fetchNotionStatus(): Promise<NotionStatus> {
  const res = await fetch(notionPath, { credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
  return (await res.json()) as NotionStatus
}

export async function disconnectNotion(): Promise<void> {
  const res = await fetch(`${notionPath}/disconnect`, { method: 'POST', credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
}

export function startNotionConnect(): void {
  window.location.assign(`${notionPath}/connect`)
}

export type GitHubStatus = {
  // false when the server has no GitHub App configured
  available: boolean
  connected: boolean
  // the login of the connected GitHub account; empty when not connected
  login: string
}

const githubPath = `${apiV1}/integrations/github`

// Throws when the request fails or the response is not 2xx.
export async function fetchGitHubStatus(): Promise<GitHubStatus> {
  const res = await fetch(githubPath, { credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
  return (await res.json()) as GitHubStatus
}

export async function disconnectGitHub(): Promise<void> {
  const res = await fetch(`${githubPath}/disconnect`, { method: 'POST', credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
}

export function startGitHubConnect(): void {
  window.location.assign(`${githubPath}/connect`)
}

// The jobs Robin can start, by the name the server uses.
export type JobName = 'hello'

export type JobSetting = {
  channel_id: string
  time_zone: string
}

export type JobTrigger = {
  id: string
  job: string
  hour: number
  minute: number
}

export type JobsStatus = {
  // null until the user saves the channel and the time zone
  setting: JobSetting | null
  triggers: JobTrigger[]
}

// Reasons the server gives for rejecting a change.
export const jobErrorCodes = ['invalid_input', 'setting_required'] as const
export type JobErrorCode = (typeof jobErrorCodes)[number]

export type JobResult<T> = { kind: 'done'; value: T } | { kind: 'rejected'; code: JobErrorCode }

const jobsPath = `${apiV1}/jobs`

// Throws when the request fails, the response is not 2xx, or the body is not
// a job status, so a broken answer shows as a failed load instead of
// breaking the page.
export async function fetchJobs(): Promise<JobsStatus> {
  const res = await fetch(jobsPath, { credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
  const body = (await res.json()) as Partial<JobsStatus>
  if (!Array.isArray(body.triggers) || body.setting === undefined) {
    throw new Error('unexpected job status')
  }
  return body as JobsStatus
}

// sendJobChange returns the reason when the server rejects the change, and
// throws for any other failure.
async function sendJobChange<T>(path: string, method: 'PUT' | 'POST', body: object): Promise<JobResult<T>> {
  const res = await fetch(path, {
    method,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (res.ok) {
    return { kind: 'done', value: (await res.json()) as T }
  }
  if (res.status === 400 || res.status === 409) {
    const data = (await res.json().catch(() => ({}))) as { error?: string }
    const code = jobErrorCodes.find((c) => c === data.error)
    if (code) {
      return { kind: 'rejected', code }
    }
  }
  throw new Error(`HTTP ${res.status}`)
}

export function saveJobSetting(setting: JobSetting): Promise<JobResult<JobSetting>> {
  return sendJobChange(`${jobsPath}/setting`, 'PUT', setting)
}

export function addJobTrigger(input: { job: JobName; hour: number; minute: number }): Promise<JobResult<JobTrigger>> {
  return sendJobChange(`${jobsPath}/triggers`, 'POST', input)
}

export async function deleteJobTrigger(id: string): Promise<void> {
  const res = await fetch(`${jobsPath}/triggers/${encodeURIComponent(id)}`, { method: 'DELETE', credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
}
