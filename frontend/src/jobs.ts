import type { Job, JobErrorCode, JobKind, JobLastRun } from './api'

export const jobKindNames: Record<JobKind, string> = {
  hello: 'Morning greeting',
}

export type StatusText = { text: string; className: 'muted' | 'success' | 'error' }

// formatTime writes a time of day as "09:05".
export function formatTime(hour: number, minute: number): string {
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
}

// formatDateTime writes an instant in the job's time zone, such as "Oct 5, 09:00".
export function formatDateTime(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-US', {
    timeZone,
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(iso))
}

export function scheduleText(job: Job): string {
  return `Every day at ${formatTime(job.hour, job.minute)} (${job.time_zone})`
}

export function nextRunText(job: Job): string {
  return `Next post: ${formatDateTime(job.next_run_at, job.time_zone)}`
}

const failureTexts: Record<Exclude<JobLastRun['failure'], ''>, string> = {
  no_runner: 'This Robin server cannot post this kind of message.',
  run_failed: 'Robin could not write or post the message.',
  timed_out: 'Robin did not finish the message in time.',
}

// lastRunText describes the last run of a job. A run still marked as running
// after its deadline has no recorded result: its process stopped, or its
// result could not be saved after the message was posted.
export function lastRunText(run: JobLastRun | null, timeZone: string, now: Date): StatusText {
  if (run === null) {
    return { text: 'Last run: not run yet', className: 'muted' }
  }
  const at = (iso: string | null) => formatDateTime(iso ?? run.scheduled_at, timeZone)
  switch (run.status) {
    case 'running':
      if (run.deadline !== null && now.getTime() > new Date(run.deadline).getTime()) {
        return {
          text: `Last run: Robin did not record the result of the post for ${at(run.scheduled_at)}. Check the channel in Slack to see whether it was posted.`,
          className: 'error',
        }
      }
      return { text: 'Last run: posting now', className: 'muted' }
    case 'succeeded':
      return { text: `Last run: posted on ${at(run.finished_at)}`, className: 'success' }
    case 'failed':
      return {
        text: `Last run: failed on ${at(run.finished_at)}. ${run.failure === '' ? failureTexts.run_failed : failureTexts[run.failure]}`,
        className: 'error',
      }
    case 'skipped':
      return {
        text: `Last run: skipped the post for ${at(run.scheduled_at)} because Robin's scheduler did not run on time.`,
        className: 'muted',
      }
  }
}

export function limitText(maxJobs: number): string {
  return `You have ${maxJobs} scheduled messages, the most allowed. Delete one to add another.`
}

// jobErrorText explains why the server did not add a job and what to do next.
export function jobErrorText(code: JobErrorCode, channelID: string, maxJobs: number): string {
  switch (code) {
    case 'invalid_input':
      return 'Check the channel ID, time, and time zone.'
    case 'channel_not_found':
      return `Robin cannot find channel ${channelID}. Check the ID. For a private channel, invite Robin to it first.`
    case 'channel_archived':
      return `Channel ${channelID} is archived. Choose a channel that is in use.`
    case 'robin_not_in_channel':
      return 'Robin is not a member of this channel. Run /invite @robin in the channel, then add it again.'
    case 'user_not_in_channel':
      return 'You can add only channels you are a member of.'
    case 'job_limit_reached':
      return limitText(maxJobs)
  }
}

// browserTimeZone is the time zone of this browser, or UTC when it is unknown.
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

// timeZoneOptions lists the time zones to choose from, always including UTC
// and the given one.
export function timeZoneOptions(current: string): string[] {
  const zones = new Set<string>(['UTC', current])
  const supported = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf
  if (supported) {
    for (const z of supported('timeZone')) {
      zones.add(z)
    }
  }
  return [...zones].sort()
}
