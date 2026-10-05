import type { JobName, JobTrigger } from './api'

// The jobs shown on the settings page, in display order.
export const jobs: { name: JobName; label: string }[] = [{ name: 'hello', label: 'Morning greeting' }]

// formatTime writes a time of day as "09:05".
export function formatTime(hour: number, minute: number): string {
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
}

// triggersOf returns the triggers of a job, earliest time first.
export function triggersOf(triggers: JobTrigger[], job: JobName): JobTrigger[] {
  return triggers.filter((t) => t.job === job).sort((a, b) => a.hour * 60 + a.minute - (b.hour * 60 + b.minute))
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
// and the given ones. The browser's list leaves out names the server accepts,
// such as US/Eastern, so a saved zone has to be given here to be shown.
export function timeZoneOptions(...include: string[]): string[] {
  const zones = new Set<string>(['UTC', ...include])
  const supported = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf
  if (supported) {
    for (const z of supported('timeZone')) {
      zones.add(z)
    }
  }
  return [...zones].sort()
}
