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

export type JobKind = 'hello'

export type JobLastRun = {
  status: 'running' | 'succeeded' | 'failed' | 'skipped'
  // why the run failed; empty unless status is "failed"
  failure: '' | 'no_runner' | 'run_failed' | 'timed_out'
  scheduled_at: string
  // when a running run gives up; null for a run that was not started
  deadline: string | null
  finished_at: string | null
}

export type Job = {
  id: string
  kind: JobKind
  channel_id: string
  channel_name: string
  hour: number
  minute: number
  time_zone: string
  next_run_at: string
  last_run: JobLastRun | null
}

export type JobsStatus = {
  // false when the server has no Slack bot token configured
  available: boolean
  max_jobs: number
  jobs: Job[]
}

export type JobInput = {
  kind: JobKind
  channel_id: string
  hour: number
  minute: number
  time_zone: string
}

// Reasons the server gives for not adding a job.
export const jobErrorCodes = [
  'invalid_input',
  'channel_not_found',
  'channel_archived',
  'robin_not_in_channel',
  'user_not_in_channel',
  'job_limit_reached',
] as const
export type JobErrorCode = (typeof jobErrorCodes)[number]

export type CreateJobResult = { kind: 'created'; job: Job } | { kind: 'rejected'; code: JobErrorCode }

const jobsPath = `${apiV1}/jobs`

// Throws when the request fails or the response is not 2xx.
export async function fetchJobs(): Promise<JobsStatus> {
  const res = await fetch(jobsPath, { credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
  return (await res.json()) as JobsStatus
}

// Returns the reason when the server rejects the job, and throws for any other
// failure.
export async function createJob(input: JobInput): Promise<CreateJobResult> {
  const res = await fetch(jobsPath, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (res.ok) {
    return { kind: 'created', job: (await res.json()) as Job }
  }
  if (res.status === 400 || res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    const code = jobErrorCodes.find((c) => c === body.error)
    if (code) {
      return { kind: 'rejected', code }
    }
  }
  throw new Error(`HTTP ${res.status}`)
}

export async function deleteJob(id: string): Promise<void> {
  const res = await fetch(`${jobsPath}/${encodeURIComponent(id)}`, { method: 'DELETE', credentials: 'include' })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
}
