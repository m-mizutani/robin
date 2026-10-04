import { describe, expect, it } from 'vitest'
import type { Job, JobLastRun } from './api'
import {
  formatDateTime,
  formatTime,
  jobErrorText,
  lastRunText,
  nextRunText,
  scheduleText,
  timeZoneOptions,
} from './jobs'

const job: Job = {
  id: '00000000-0000-4000-8000-000000000001',
  kind: 'hello',
  channel_id: 'C0GENERAL',
  channel_name: 'general',
  hour: 9,
  minute: 5,
  time_zone: 'Asia/Tokyo',
  next_run_at: '2026-10-05T00:05:00Z',
  last_run: null,
}

function run(overrides: Partial<JobLastRun>): JobLastRun {
  return {
    status: 'succeeded',
    failure: '',
    scheduled_at: '2026-10-04T00:05:00Z',
    deadline: '2026-10-04T00:08:00Z',
    finished_at: '2026-10-04T00:05:20Z',
    ...overrides,
  }
}

const tokyo = 'Asia/Tokyo'
const now = new Date('2026-10-04T00:06:00Z')

describe('formatting', () => {
  it('writes times of day with two digits', () => {
    expect(formatTime(9, 5)).toBe('09:05')
    expect(formatTime(23, 0)).toBe('23:00')
  })

  it('writes instants in the time zone of the job', () => {
    expect(formatDateTime('2026-10-05T00:05:00Z', tokyo)).toBe('Oct 5, 09:05')
    expect(formatDateTime('2026-10-05T00:05:00Z', 'UTC')).toBe('Oct 5, 00:05')
  })

  it('describes the schedule and the next post', () => {
    expect(scheduleText(job)).toBe('Every day at 09:05 (Asia/Tokyo)')
    expect(nextRunText(job)).toBe('Next post: Oct 5, 09:05')
  })
})

describe('lastRunText', () => {
  it('covers every state of the last run', () => {
    expect(lastRunText(null, tokyo, now)).toEqual({ text: 'Last run: not run yet', className: 'muted' })
    expect(lastRunText(run({ status: 'running', finished_at: null }), tokyo, now)).toEqual({
      text: 'Last run: posting now',
      className: 'muted',
    })
    expect(lastRunText(run({ status: 'running', finished_at: null }), tokyo, new Date('2026-10-04T00:09:00Z'))).toEqual({
      text: 'Last run: Robin did not record the result of the post for Oct 4, 09:05. Check the channel in Slack to see whether it was posted.',
      className: 'error',
    })
    expect(lastRunText(run({}), tokyo, now)).toEqual({ text: 'Last run: posted on Oct 4, 09:05', className: 'success' })
    expect(lastRunText(run({ status: 'failed', failure: 'run_failed' }), tokyo, now)).toEqual({
      text: 'Last run: failed on Oct 4, 09:05. Robin could not write or post the message.',
      className: 'error',
    })
    expect(lastRunText(run({ status: 'skipped', deadline: null, finished_at: '2026-10-04T02:00:00Z' }), tokyo, now)).toEqual({
      text: "Last run: skipped the post for Oct 4, 09:05 because Robin's scheduler did not run on time.",
      className: 'muted',
    })
  })

  it('names the reason of a failure', () => {
    expect(lastRunText(run({ status: 'failed', failure: 'timed_out' }), tokyo, now).text).toContain(
      'Robin did not finish the message in time.',
    )
    expect(lastRunText(run({ status: 'failed', failure: 'no_runner' }), tokyo, now).text).toContain(
      'This Robin server cannot post this kind of message.',
    )
  })
})

describe('jobErrorText', () => {
  it('tells the user what to do for each rejection', () => {
    expect(jobErrorText('invalid_input', 'C1', 10)).toBe('Check the channel ID, time, and time zone.')
    expect(jobErrorText('channel_not_found', 'C0MISSING', 10)).toBe(
      'Robin cannot find channel C0MISSING. Check the ID. For a private channel, invite Robin to it first.',
    )
    expect(jobErrorText('channel_archived', 'C0OLD', 10)).toBe('Channel C0OLD is archived. Choose a channel that is in use.')
    expect(jobErrorText('robin_not_in_channel', 'C1', 10)).toBe(
      'Robin is not a member of this channel. Run /invite @robin in the channel, then add it again.',
    )
    expect(jobErrorText('user_not_in_channel', 'C1', 10)).toBe('You can add only channels you are a member of.')
    expect(jobErrorText('job_limit_reached', 'C1', 10)).toBe(
      'You have 10 scheduled messages, the most allowed. Delete one to add another.',
    )
  })
})

describe('timeZoneOptions', () => {
  it('includes UTC and the given zone once, sorted', () => {
    const zones = timeZoneOptions('Asia/Tokyo')
    expect(zones).toContain('UTC')
    expect(zones.filter((z) => z === 'Asia/Tokyo')).toHaveLength(1)
    expect([...zones].sort()).toEqual(zones)
  })
})
