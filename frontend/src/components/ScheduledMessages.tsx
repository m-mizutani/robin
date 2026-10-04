import { type FormEvent, useCallback, useEffect, useMemo, useState } from 'react'
import { addJobTrigger, deleteJobTrigger, fetchJobs, type JobName, type JobsStatus, saveJobSetting } from '../api'
import type { ServiceState } from '../integrations'
import { browserTimeZone, formatTime, jobs, timeZoneOptions, triggersOf } from '../jobs'

const channelIDPattern = '[CG][A-Z0-9]{2,}'
const defaultTime = '09:00'

type Notice = { text: string; className: 'success' | 'error'; role: 'status' | 'alert' }

// ScheduledMessages saves the channel and the time zone of the user's jobs,
// and adds or deletes the times each job starts. It is the first section of
// the settings page.
export default function ScheduledMessages() {
  const [state, setState] = useState<ServiceState<JobsStatus>>({ kind: 'loading' })
  const [channelID, setChannelID] = useState('')
  const [timeZone, setTimeZone] = useState(browserTimeZone)
  const [saving, setSaving] = useState(false)
  const [saveNotice, setSaveNotice] = useState<Notice | null>(null)
  const [times, setTimes] = useState<Record<string, string>>({})
  const [adding, setAdding] = useState<JobName | null>(null)
  const [addError, setAddError] = useState<{ job: JobName; text: string } | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [deleteFailed, setDeleteFailed] = useState<string | null>(null)
  const savedZone = state.kind === 'loaded' ? state.status.setting?.time_zone : undefined
  const zones = useMemo(() => timeZoneOptions(browserTimeZone(), ...(savedZone ? [savedZone] : [])), [savedZone])

  const load = useCallback(async () => {
    setState({ kind: 'loading' })
    try {
      const status = await fetchJobs()
      setState({ kind: 'loaded', status })
      if (status.setting) {
        setChannelID(status.setting.channel_id)
        setTimeZone(status.setting.time_zone)
      }
    } catch {
      setState({ kind: 'error' })
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const update = (change: (s: JobsStatus) => JobsStatus) => {
    setState((s) => (s.kind === 'loaded' ? { kind: 'loaded', status: change(s.status) } : s))
  }

  const onSave = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    setSaving(true)
    setSaveNotice(null)
    try {
      const result = await saveJobSetting({ channel_id: channelID, time_zone: timeZone })
      if (result.kind === 'rejected') {
        setSaveNotice({ text: 'Check the channel ID and time zone.', className: 'error', role: 'alert' })
        return
      }
      update((s) => ({ ...s, setting: result.value }))
      setSaveNotice({ text: 'Saved.', className: 'success', role: 'status' })
    } catch {
      setSaveNotice({ text: 'Could not save. Try again.', className: 'error', role: 'alert' })
    } finally {
      setSaving(false)
    }
  }

  const onAdd = async (e: FormEvent<HTMLFormElement>, job: JobName) => {
    e.preventDefault()
    const [hour, minute] = (times[job] ?? defaultTime).split(':').map(Number)
    setAdding(job)
    setAddError(null)
    try {
      const result = await addJobTrigger({ job, hour, minute })
      if (result.kind === 'rejected') {
        const text = result.code === 'setting_required' ? 'Save the channel and time zone first.' : 'Check the time.'
        setAddError({ job, text })
        return
      }
      update((s) => ({ ...s, triggers: [...s.triggers, result.value] }))
    } catch {
      setAddError({ job, text: 'Could not add the time. Try again.' })
    } finally {
      setAdding(null)
    }
  }

  const onDelete = async (id: string) => {
    setDeleting(id)
    setDeleteFailed(null)
    try {
      await deleteJobTrigger(id)
      update((s) => ({ ...s, triggers: s.triggers.filter((t) => t.id !== id) }))
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
    const hasSetting = status.setting !== null
    return (
      <>
        <form className="job-form" onSubmit={(e) => void onSave(e)}>
          <div className="field">
            <label htmlFor="job-channel-id">Channel ID</label>
            <input
              id="job-channel-id"
              value={channelID}
              onChange={(e) => {
                setChannelID(e.target.value.trim())
                // "Saved." describes the values that were saved, not new input.
                setSaveNotice(null)
              }}
              placeholder="C0123ABCD"
              pattern={channelIDPattern}
              title="A channel ID starts with C or G, followed by capital letters and digits."
              required
              disabled={saving}
              aria-describedby="job-channel-hint"
            />
            <p id="job-channel-hint" className="muted">
              Find the channel ID at the bottom of the channel details in Slack. Invite Robin to the channel.
            </p>
          </div>
          <div className="field">
            <label htmlFor="job-time-zone">Time zone</label>
            <select id="job-time-zone" value={timeZone} onChange={(e) => {
                setTimeZone(e.target.value)
                setSaveNotice(null)
              }}
              disabled={saving}
            >
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </select>
          </div>
          {saveNotice && (
            <p className={saveNotice.className} role={saveNotice.role}>
              {saveNotice.text}
            </p>
          )}
          <button type="submit" className="button" disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </form>
        {jobs.map((job) => {
          const triggers = triggersOf(status.triggers, job.name)
          const isAdding = adding === job.name
          return (
            <section key={job.name} className="job" aria-labelledby={`job-${job.name}`}>
              <p id={`job-${job.name}`} className="job-name">
                {job.label}
              </p>
              {triggers.length === 0 ? (
                <p className="muted">No times yet.</p>
              ) : (
                <ul className="trigger-list">
                  {triggers.map((t) => {
                    const time = formatTime(t.hour, t.minute)
                    return (
                      <li key={t.id} className="trigger" aria-label={time}>
                        <span className="trigger-time">{time}</span>
                        <button
                          type="button"
                          className="button secondary"
                          aria-label={`Delete ${time}`}
                          onClick={() => void onDelete(t.id)}
                          // One deletion at a time, so the progress and the
                          // error shown belong to the time the user acted on.
                          disabled={deleting !== null}
                        >
                          {deleting === t.id ? 'Deleting…' : 'Delete'}
                        </button>
                        {deleteFailed === t.id && (
                          <p className="error" role="alert">
                            Could not delete the time. Try again.
                          </p>
                        )}
                      </li>
                    )
                  })}
                </ul>
              )}
              <form className="trigger-form" onSubmit={(e) => void onAdd(e, job.name)}>
                <div className="field">
                  <label htmlFor={`job-${job.name}-time`}>Time</label>
                  <input
                    id={`job-${job.name}-time`}
                    type="time"
                    value={times[job.name] ?? defaultTime}
                    onChange={(e) => setTimes((cur) => ({ ...cur, [job.name]: e.target.value }))}
                    required
                    disabled={!hasSetting || isAdding}
                  />
                </div>
                <button type="submit" className="button" disabled={!hasSetting || isAdding}>
                  {isAdding ? 'Adding…' : 'Add'}
                </button>
              </form>
              {!hasSetting && <p className="muted">Save the channel and time zone first.</p>}
              {addError?.job === job.name && (
                <p className="error" role="alert">
                  {addError.text}
                </p>
              )}
            </section>
          )
        })}
      </>
    )
  }

  return (
    <section className="section" aria-labelledby="scheduled-messages">
      <h2 id="scheduled-messages">Scheduled messages</h2>
      <p className="muted">Robin posts messages to your Slack channel at the times you set.</p>
      {renderBody()}
    </section>
  )
}
