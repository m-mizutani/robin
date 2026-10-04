import { type FormEvent, useCallback, useEffect, useMemo, useState } from 'react'
import { createJob, deleteJob, fetchJobs, type Job, type JobsStatus } from '../api'
import type { ServiceState } from '../integrations'
import {
  browserTimeZone,
  jobErrorText,
  jobKindNames,
  lastRunText,
  limitText,
  nextRunText,
  scheduleText,
  timeZoneOptions,
} from '../jobs'

const channelIDPattern = '[CG][A-Z0-9]{2,}'
const defaultTime = '09:00'

type Props = {
  // now returns the current time; tests pass a fixed clock.
  now?: () => Date
}

// ScheduledMessages lists the user's scheduled messages and adds or deletes
// them. It is the first section of the settings page.
export default function ScheduledMessages({ now = () => new Date() }: Props) {
  const [state, setState] = useState<ServiceState<JobsStatus>>({ kind: 'loading' })
  const [channelID, setChannelID] = useState('')
  const [time, setTime] = useState(defaultTime)
  const [timeZone, setTimeZone] = useState(browserTimeZone)
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [deleteFailed, setDeleteFailed] = useState<string | null>(null)
  const zones = useMemo(() => timeZoneOptions(browserTimeZone()), [])

  const load = useCallback(async () => {
    setState({ kind: 'loading' })
    try {
      setState({ kind: 'loaded', status: await fetchJobs() })
    } catch {
      setState({ kind: 'error' })
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const updateJobs = (update: (jobs: Job[]) => Job[]) => {
    setState((s) => (s.kind === 'loaded' ? { kind: 'loaded', status: { ...s.status, jobs: update(s.status.jobs) } } : s))
  }

  const onAdd = async (e: FormEvent<HTMLFormElement>, maxJobs: number) => {
    e.preventDefault()
    const [hour, minute] = time.split(':').map(Number)
    setAdding(true)
    setAddError(null)
    try {
      const result = await createJob({ kind: 'hello', channel_id: channelID, hour, minute, time_zone: timeZone })
      if (result.kind === 'rejected') {
        setAddError(jobErrorText(result.code, channelID, maxJobs))
        return
      }
      updateJobs((jobs) => [...jobs, result.job])
      setChannelID('')
    } catch {
      setAddError('Could not add the scheduled message. Try again.')
    } finally {
      setAdding(false)
    }
  }

  const onDelete = async (id: string) => {
    setDeleting(id)
    setDeleteFailed(null)
    try {
      await deleteJob(id)
      updateJobs((jobs) => jobs.filter((j) => j.id !== id))
    } catch {
      setDeleteFailed(id)
    } finally {
      setDeleting(null)
    }
  }

  const renderBody = () => {
    switch (state.kind) {
      case 'loading':
        return <p className="muted">Loading scheduled messages…</p>
      case 'error':
        return (
          <>
            <p className="error" role="alert">
              Could not load your scheduled messages.
            </p>
            <button type="button" className="button secondary" onClick={() => void load()}>
              Load again
            </button>
          </>
        )
      case 'loaded':
        break
    }
    const { status } = state
    if (!status.available) {
      return <p className="muted">Your Robin administrator has not set up scheduled messages.</p>
    }
    const atLimit = status.jobs.length >= status.max_jobs
    return (
      <>
        {status.jobs.length === 0 ? (
          <p className="muted">No scheduled messages yet.</p>
        ) : (
          <ul className="job-list">
            {status.jobs.map((job) => {
              const name = `${jobKindNames[job.kind] ?? job.kind} in #${job.channel_name}`
              const last = lastRunText(job.last_run, job.time_zone, now())
              const isDeleting = deleting === job.id
              return (
                <li key={job.id} className="job" aria-label={name}>
                  <p className="job-name">{name}</p>
                  <p className="muted">{scheduleText(job)}</p>
                  <p className="muted">{nextRunText(job)}</p>
                  <p className={last.className}>{last.text}</p>
                  {deleteFailed === job.id && (
                    <p className="error" role="alert">
                      Could not delete this scheduled message. Try again.
                    </p>
                  )}
                  <button
                    type="button"
                    className="button secondary"
                    aria-label={`Delete ${name}`}
                    onClick={() => void onDelete(job.id)}
                    // One deletion at a time, so the progress and the error
                    // shown belong to the row the user acted on.
                    disabled={deleting !== null}
                  >
                    {isDeleting ? 'Deleting…' : 'Delete'}
                  </button>
                </li>
              )
            })}
          </ul>
        )}
        {atLimit ? (
          <p className="muted">{limitText(status.max_jobs)}</p>
        ) : (
          <form className="job-form" onSubmit={(e) => void onAdd(e, status.max_jobs)}>
            <div className="field">
              <label htmlFor="job-channel-id">Channel ID</label>
              <input
                id="job-channel-id"
                value={channelID}
                onChange={(e) => setChannelID(e.target.value.trim())}
                placeholder="C0123ABCD"
                pattern={channelIDPattern}
                title="A channel ID starts with C or G, followed by capital letters and digits."
                required
                disabled={adding}
                aria-describedby="job-channel-hint"
              />
              <p id="job-channel-hint" className="muted">
                Find the channel ID at the bottom of the channel details in Slack. Invite Robin to the channel first.
              </p>
            </div>
            <div className="field-row">
              <div className="field">
                <label htmlFor="job-time">Time</label>
                <input
                  id="job-time"
                  type="time"
                  value={time}
                  onChange={(e) => setTime(e.target.value)}
                  required
                  disabled={adding}
                />
              </div>
              <div className="field">
                <label htmlFor="job-time-zone">Time zone</label>
                <select
                  id="job-time-zone"
                  value={timeZone}
                  onChange={(e) => setTimeZone(e.target.value)}
                  disabled={adding}
                >
                  {zones.map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            {addError && (
              <p className="error" role="alert">
                {addError}
              </p>
            )}
            <button type="submit" className="button" disabled={adding}>
              {adding ? 'Adding…' : 'Add'}
            </button>
          </form>
        )}
      </>
    )
  }

  return (
    <section className="section" aria-labelledby="scheduled-messages">
      <h2 id="scheduled-messages">Scheduled messages</h2>
      <p className="muted">
        Robin posts a short greeting written by Claude to a Slack channel every day at the time you choose.
      </p>
      {renderBody()}
    </section>
  )
}
