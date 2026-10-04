import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Job, JobsStatus } from '../api'
import ScheduledMessages from './ScheduledMessages'

const jobsPath = '/api/v1/jobs'

function job(overrides: Partial<Job> = {}): Job {
  return {
    id: '00000000-0000-4000-8000-000000000001',
    kind: 'hello',
    channel_id: 'C0GENERAL',
    channel_name: 'general',
    hour: 9,
    minute: 0,
    time_zone: 'Asia/Tokyo',
    next_run_at: '2026-10-05T00:00:00Z',
    last_run: null,
    ...overrides,
  }
}

function status(jobs: Job[], overrides: Partial<JobsStatus> = {}): string {
  return JSON.stringify({ available: true, max_jobs: 10, jobs, ...overrides })
}

type Handler = (url: string, init?: RequestInit) => Promise<Response>

function stubFetch(handler: Handler) {
  const mock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => handler(String(input), init))
  vi.stubGlobal('fetch', mock)
  return mock
}

// Answers GET /api/v1/jobs with list and sends the other requests to rest.
function stubJobs(list: string, rest?: Handler) {
  return stubFetch(async (url, init) => {
    if (url === jobsPath && (init?.method ?? 'GET') === 'GET') {
      return new Response(list, { status: 200 })
    }
    if (rest) {
      return rest(url, init)
    }
    return new Response('{"error":"not_found"}', { status: 404 })
  })
}

const fixedNow = () => new Date('2026-10-04T23:30:00Z')

function renderSection() {
  return render(<ScheduledMessages now={fixedNow} />)
}

async function fillForm(channelID: string, time = '08:30', zone = 'UTC') {
  fireEvent.change(await screen.findByLabelText('Channel ID'), { target: { value: channelID } })
  fireEvent.change(screen.getByLabelText('Time'), { target: { value: time } })
  fireEvent.change(screen.getByLabelText('Time zone'), { target: { value: zone } })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ScheduledMessages', () => {
  it('shows the loading state until the list arrives', async () => {
    stubFetch(() => new Promise<Response>(() => {}))
    renderSection()
    expect(screen.getByRole('heading', { name: 'Scheduled messages', level: 2 })).toBeInTheDocument()
    expect(screen.getByText('Loading scheduled messages…')).toBeInTheDocument()
  })

  it('offers to load the list again after a failure', async () => {
    let fail = true
    stubFetch(async () => (fail ? new Response('{"error":"internal_error"}', { status: 500 }) : new Response(status([]))))
    renderSection()

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load your scheduled messages.')
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Load again' }))
    expect(await screen.findByText('No scheduled messages yet.')).toBeInTheDocument()
  })

  it('explains that the feature is not set up', async () => {
    stubJobs(status([], { available: false, max_jobs: 0 }))
    renderSection()
    expect(await screen.findByText('Your Robin administrator has not set up scheduled messages.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Channel ID')).toBeNull()
  })

  it('shows the empty state with the form', async () => {
    stubJobs(status([]))
    renderSection()
    expect(await screen.findByText('No scheduled messages yet.')).toBeInTheDocument()
    expect(screen.getByLabelText('Channel ID')).toHaveValue('')
    expect(screen.getByLabelText('Time')).toHaveValue('09:00')
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
  })

  it('lists each job with its schedule, next post and last run', async () => {
    stubJobs(
      status([
        job(),
        job({
          id: '00000000-0000-4000-8000-000000000002',
          channel_name: 'random',
          last_run: {
            status: 'succeeded',
            failure: '',
            scheduled_at: '2026-10-04T00:00:00Z',
            deadline: '2026-10-04T00:02:00Z',
            finished_at: '2026-10-04T00:00:12Z',
          },
        }),
      ]),
    )
    renderSection()

    const general = within(await screen.findByRole('listitem', { name: 'Morning greeting in #general' }))
    expect(general.getByText('Every day at 09:00 (Asia/Tokyo)')).toBeInTheDocument()
    expect(general.getByText('Next post: Oct 5, 09:00')).toBeInTheDocument()
    expect(general.getByText('Last run: not run yet')).toBeInTheDocument()

    const random = within(screen.getByRole('listitem', { name: 'Morning greeting in #random' }))
    expect(random.getByText('Last run: posted on Oct 4, 09:00')).toHaveClass('success')
  })

  it('shows a run still marked as running past its deadline as unrecorded', async () => {
    stubJobs(
      status([
        job({
          last_run: {
            status: 'running',
            failure: '',
            scheduled_at: '2026-10-04T00:00:00Z',
            deadline: '2026-10-04T00:02:00Z',
            finished_at: null,
          },
        }),
      ]),
    )
    renderSection()
    expect(await screen.findByText(/Last run: Robin did not record the result/)).toHaveClass('error')
  })

  it('adds a job and clears the channel ID', async () => {
    const created = job({ id: '00000000-0000-4000-8000-000000000009', channel_id: 'C0NEW', channel_name: 'new' })
    const mock = stubJobs(status([]), async () => new Response(JSON.stringify(created), { status: 201 }))
    renderSection()

    await fillForm('C0NEW')
    fireEvent.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByRole('listitem', { name: 'Morning greeting in #new' })).toBeInTheDocument()
    expect(screen.getByLabelText('Channel ID')).toHaveValue('')
    expect(screen.getByLabelText('Time')).toHaveValue('08:30')
    expect(screen.queryByText('No scheduled messages yet.')).toBeNull()

    const posts = mock.mock.calls.filter(([, init]) => init?.method === 'POST')
    expect(posts).toHaveLength(1)
    expect(JSON.parse(String(posts[0][1]?.body))).toEqual({
      kind: 'hello',
      channel_id: 'C0NEW',
      hour: 8,
      minute: 30,
      time_zone: 'UTC',
    })
  })

  it('disables the form while adding', async () => {
    stubJobs(status([]), () => new Promise<Response>(() => {}))
    renderSection()
    await fillForm('C0NEW')
    fireEvent.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByRole('button', { name: 'Adding…' })).toBeDisabled()
    expect(screen.getByLabelText('Channel ID')).toBeDisabled()
    expect(screen.getByLabelText('Time')).toBeDisabled()
    expect(screen.getByLabelText('Time zone')).toBeDisabled()
  })

  it.each([
    ['robin_not_in_channel', 400, 'Robin is not a member of this channel. Run /invite @robin in the channel, then add it again.'],
    ['user_not_in_channel', 400, 'You can add only channels you are a member of.'],
    ['channel_not_found', 400, 'Robin cannot find channel C0NEW. Check the ID. For a private channel, invite Robin to it first.'],
    ['channel_archived', 400, 'Channel C0NEW is archived. Choose a channel that is in use.'],
    ['invalid_input', 400, 'Check the channel ID, time, and time zone.'],
    ['job_limit_reached', 409, 'You have 10 scheduled messages, the most allowed. Delete one to add another.'],
    ['internal_error', 500, 'Could not add the scheduled message. Try again.'],
  ])('shows why %s kept the job from being added and keeps the input', async (code, httpStatus, message) => {
    stubJobs(status([]), async () => new Response(JSON.stringify({ error: code }), { status: httpStatus }))
    renderSection()
    await fillForm('C0NEW')
    fireEvent.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(message)
    expect(screen.getByLabelText('Channel ID')).toHaveValue('C0NEW')
    expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled()
    expect(screen.getByText('No scheduled messages yet.')).toBeInTheDocument()
  })

  it('replaces the form with the limit when the user has the most jobs', async () => {
    const jobs = Array.from({ length: 2 }, (_, i) =>
      job({ id: `00000000-0000-4000-8000-00000000000${i}`, channel_name: `c${i}` }),
    )
    stubJobs(status(jobs, { max_jobs: 2 }))
    renderSection()
    expect(await screen.findByText('You have 2 scheduled messages, the most allowed. Delete one to add another.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Channel ID')).toBeNull()
  })

  it('deletes a job, one at a time', async () => {
    let release: (r: Response) => void = () => {}
    const mock = stubJobs(
      status([job(), job({ id: '00000000-0000-4000-8000-000000000002', channel_name: 'random' })]),
      () => new Promise<Response>((resolve) => (release = resolve)),
    )
    renderSection()

    const row = within(await screen.findByRole('listitem', { name: 'Morning greeting in #general' }))
    fireEvent.click(row.getByRole('button', { name: 'Delete Morning greeting in #general' }))
    expect(await row.findByRole('button', { name: 'Delete Morning greeting in #general' })).toHaveTextContent('Deleting…')
    expect(row.getByRole('button', { name: 'Delete Morning greeting in #general' })).toBeDisabled()
    const other = screen.getByRole('button', { name: 'Delete Morning greeting in #random' })
    expect(other).toHaveTextContent('Delete')
    expect(other).toBeDisabled()

    release(new Response('{"success":true}', { status: 200 }))
    await waitFor(() => expect(screen.queryByRole('listitem', { name: 'Morning greeting in #general' })).toBeNull())
    expect(screen.getByRole('button', { name: 'Delete Morning greeting in #random' })).toBeEnabled()
    const deletes = mock.mock.calls.filter(([, init]) => init?.method === 'DELETE')
    expect(deletes.map(([url]) => String(url))).toEqual(['/api/v1/jobs/00000000-0000-4000-8000-000000000001'])
  })

  it('keeps a job whose deletion failed and says so', async () => {
    stubJobs(status([job()]), async () => new Response('{"error":"internal_error"}', { status: 500 }))
    renderSection()

    const row = within(await screen.findByRole('listitem', { name: 'Morning greeting in #general' }))
    fireEvent.click(row.getByRole('button', { name: 'Delete Morning greeting in #general' }))
    expect(await row.findByRole('alert')).toHaveTextContent('Could not delete this scheduled message. Try again.')
    expect(row.getByRole('button', { name: 'Delete Morning greeting in #general' })).toBeEnabled()
  })
})
