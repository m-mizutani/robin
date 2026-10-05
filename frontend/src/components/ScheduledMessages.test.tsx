import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { JobsStatus, JobTrigger } from '../api'
import ScheduledMessages from './ScheduledMessages'

const jobsPath = '/api/v1/jobs'

function trigger(id: string, hour: number, minute = 0): JobTrigger {
  return { id, job: 'hello', hour, minute }
}

function status(s: Partial<JobsStatus> = {}): string {
  return JSON.stringify({ setting: { channel_id: 'C0GENERAL', time_zone: 'Asia/Tokyo' }, triggers: [], ...s })
}

const noSetting = JSON.stringify({ setting: null, triggers: [] })

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

const json = (body: object, s = 200) => new Response(JSON.stringify(body), { status: s })

const greeting = () => within(screen.getByRole('region', { name: 'Morning greeting' }))

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ScheduledMessages', () => {
  it('shows the loading state until the setting arrives', () => {
    stubFetch(() => new Promise<Response>(() => {}))
    render(<ScheduledMessages />)
    expect(screen.getByRole('heading', { name: 'Scheduled messages', level: 2 })).toBeInTheDocument()
    expect(screen.getByText('Loading scheduled messages…')).toBeInTheDocument()
  })

  it('offers to load again after a failure', async () => {
    let fail = true
    stubFetch(async () => (fail ? json({ error: 'internal_error' }, 500) : new Response(noSetting)))
    render(<ScheduledMessages />)

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load your scheduled messages.')
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Load again' }))
    expect(await screen.findByLabelText('Channel ID')).toBeInTheDocument()
  })

  it('treats an answer that is not a job status as a failed load', async () => {
    stubJobs('{"team_id":"T1"}')
    render(<ScheduledMessages />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load your scheduled messages.')
  })

  it('without a setting, asks to save the channel and time zone before adding a time', async () => {
    stubJobs(noSetting)
    render(<ScheduledMessages />)

    expect(await screen.findByLabelText('Channel ID')).toHaveValue('')
    expect(screen.getByLabelText('Time zone')).not.toHaveValue('')
    expect(greeting().getByText('No times yet.')).toBeInTheDocument()
    expect(greeting().getByText('Save the channel and time zone first.')).toBeInTheDocument()
    expect(greeting().getByRole('button', { name: 'Add' })).toBeDisabled()
    expect(greeting().getByLabelText('Time')).toBeDisabled()
  })

  it('shows the saved setting and the times, earliest first', async () => {
    stubJobs(status({ triggers: [trigger('t2', 18, 30), trigger('t1', 9)] }))
    render(<ScheduledMessages />)

    expect(await screen.findByLabelText('Channel ID')).toHaveValue('C0GENERAL')
    expect(screen.getByLabelText('Time zone')).toHaveValue('Asia/Tokyo')
    expect(greeting().getAllByRole('listitem').map((li) => li.getAttribute('aria-label'))).toEqual(['09:00', '18:30'])
    expect(greeting().getByRole('button', { name: 'Add' })).toBeEnabled()
    expect(greeting().queryByText('Save the channel and time zone first.')).toBeNull()
  })

  it('saves the setting and then allows adding a time', async () => {
    let release: (r: Response) => void = () => {}
    const mock = stubJobs(noSetting, () => new Promise<Response>((resolve) => (release = resolve)))
    render(<ScheduledMessages />)

    fireEvent.change(await screen.findByLabelText('Channel ID'), { target: { value: 'C0NEW' } })
    fireEvent.change(screen.getByLabelText('Time zone'), { target: { value: 'UTC' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('button', { name: 'Saving…' })).toBeDisabled()
    expect(screen.getByLabelText('Channel ID')).toBeDisabled()
    release(json({ channel_id: 'C0NEW', time_zone: 'UTC' }))

    expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
    expect(greeting().getByRole('button', { name: 'Add' })).toBeEnabled()
    const puts = mock.mock.calls.filter(([, init]) => init?.method === 'PUT')
    expect(puts.map(([url]) => String(url))).toEqual(['/api/v1/jobs/setting'])
    expect(JSON.parse(String(puts[0][1]?.body))).toEqual({ channel_id: 'C0NEW', time_zone: 'UTC' })
  })

  it('shows a saved time zone that the browser does not list', async () => {
    stubJobs(status({ setting: { channel_id: 'C0GENERAL', time_zone: 'US/Eastern' } }))
    render(<ScheduledMessages />)
    expect(await screen.findByLabelText('Time zone')).toHaveValue('US/Eastern')
  })

  it('removes "Saved." once the channel or the time zone is changed again', async () => {
    stubJobs(status(), async () => json({ channel_id: 'C0GENERAL', time_zone: 'UTC' }))
    render(<ScheduledMessages />)

    fireEvent.change(await screen.findByLabelText('Time zone'), { target: { value: 'UTC' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
    fireEvent.change(screen.getByLabelText('Channel ID'), { target: { value: 'C0OTHER' } })
    expect(screen.queryByRole('status')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
    fireEvent.change(screen.getByLabelText('Time zone'), { target: { value: 'Asia/Tokyo' } })
    expect(screen.queryByRole('status')).toBeNull()
  })

  it.each([
    [400, { error: 'invalid_input' }, 'Check the channel ID and time zone.'],
    [500, { error: 'internal_error' }, 'Could not save. Try again.'],
  ])('shows why saving failed (%i) and keeps the input', async (code, body, message) => {
    stubJobs(noSetting, async () => json(body, code))
    render(<ScheduledMessages />)
    fireEvent.change(await screen.findByLabelText('Channel ID'), { target: { value: 'C0NEW' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(message)
    expect(screen.getByLabelText('Channel ID')).toHaveValue('C0NEW')
    expect(greeting().getByRole('button', { name: 'Add' })).toBeDisabled()
  })

  it('adds a time', async () => {
    let release: (r: Response) => void = () => {}
    const mock = stubJobs(status(), () => new Promise<Response>((resolve) => (release = resolve)))
    render(<ScheduledMessages />)

    fireEvent.change(await screen.findByLabelText('Time'), { target: { value: '08:30' } })
    fireEvent.click(greeting().getByRole('button', { name: 'Add' }))
    expect(await greeting().findByRole('button', { name: 'Adding…' })).toBeDisabled()
    release(json(trigger('t1', 8, 30), 201))

    expect(await greeting().findByRole('listitem', { name: '08:30' })).toBeInTheDocument()
    expect(greeting().queryByText('No times yet.')).toBeNull()
    const posts = mock.mock.calls.filter(([, init]) => init?.method === 'POST')
    expect(posts.map(([url]) => String(url))).toEqual(['/api/v1/jobs/triggers'])
    expect(JSON.parse(String(posts[0][1]?.body))).toEqual({ job: 'hello', hour: 8, minute: 30 })
  })

  it.each([
    [400, { error: 'invalid_input' }, 'Check the time.'],
    [409, { error: 'setting_required' }, 'Save the channel and time zone first.'],
    [500, { error: 'internal_error' }, 'Could not add the time. Try again.'],
  ])('shows why adding a time failed (%i)', async (code, body, message) => {
    stubJobs(status(), async () => json(body, code))
    render(<ScheduledMessages />)
    await screen.findByRole('region', { name: 'Morning greeting' })
    fireEvent.click(greeting().getByRole('button', { name: 'Add' }))

    expect(await greeting().findByRole('alert')).toHaveTextContent(message)
    expect(greeting().getByText('No times yet.')).toBeInTheDocument()
  })

  it('deletes a time, one at a time', async () => {
    let release: (r: Response) => void = () => {}
    const mock = stubJobs(status({ triggers: [trigger('t1', 9), trigger('t2', 18)] }), () =>
      new Promise<Response>((resolve) => (release = resolve)),
    )
    render(<ScheduledMessages />)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete 09:00' }))
    expect(await screen.findByRole('button', { name: 'Delete 09:00' })).toHaveTextContent('Deleting…')
    expect(screen.getByRole('button', { name: 'Delete 09:00' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete 18:00' })).toBeDisabled()

    release(json({ success: true }))
    await waitFor(() => expect(screen.queryByRole('listitem', { name: '09:00' })).toBeNull())
    expect(screen.getByRole('button', { name: 'Delete 18:00' })).toBeEnabled()
    const deletes = mock.mock.calls.filter(([, init]) => init?.method === 'DELETE')
    expect(deletes.map(([url]) => String(url))).toEqual(['/api/v1/jobs/triggers/t1'])
  })

  it('keeps a time whose deletion failed and says so', async () => {
    stubJobs(status({ triggers: [trigger('t1', 9)] }), async () => json({ error: 'internal_error' }, 500))
    render(<ScheduledMessages />)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete 09:00' }))
    const row = within(screen.getByRole('listitem', { name: '09:00' }))
    expect(await row.findByRole('alert')).toHaveTextContent('Could not delete the time. Try again.')
    expect(row.getByRole('button', { name: 'Delete 09:00' })).toBeEnabled()
  })
})
